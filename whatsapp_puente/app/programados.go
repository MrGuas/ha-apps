// Mensajes programados: cola en SQLite que la propia App envía a su hora.
//
// Reglas de seguridad según quién programa el mensaje:
//   - origen "pantalla": el propio usuario desde la interfaz (Ingress) → sin restricciones.
//   - origen "api":      un asistente o integración por la API → SIEMPRE queda "por_confirmar"
//     hasta que el usuario pulse "Enviar" en la notificación del móvil (o en la pantalla).
//     (los registros antiguos con origen "claude" se tratan igual.)
//   - origen "auto":     automatizaciones que se repiten → solo a destinos de la lista blanca;
//     si el destino no está en la lista, también pasa a "por_confirmar".
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

type programado struct {
	ID         int64  `json:"id"`
	Chat       string `json:"chat"`
	ChatNombre string `json:"chat_nombre"`
	Texto      string `json:"texto"`
	Cuando     string `json:"cuando"`
	CuandoTS   int64  `json:"cuando_ts"`
	Origen     string `json:"origen"`
	Estado     string `json:"estado"`
	Enviado    string `json:"enviado,omitempty"`
	Error      string `json:"error,omitempty"`
}

const fmtHora = "02/01/2006 15:04"

func (a *app) routesProgramados(mux *http.ServeMux) {
	mux.HandleFunc("/ui/programar", a.onlyIngress(a.handleUIProgramar)) // desde la pantalla: sin restricciones
	mux.HandleFunc("/ui/confirmar", a.onlyIngress(a.handleConfirmar))
	mux.HandleFunc("/api/programar", a.auth(a.handleAPIProgramar)) // asistentes / automatizaciones
	mux.HandleFunc("/api/send", a.auth(a.handleAPIProgramar))      // compatibilidad: igual que programar "ahora"
	mux.HandleFunc("/api/programados", a.auth(a.handleListar))
	mux.HandleFunc("/api/cancelar", a.auth(a.handleCancelar))
	mux.HandleFunc("/api/contactos", a.auth(a.handleContactos))
}

func (a *app) onlyIngress(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !fromIngress(r) {
			http.Error(w, "solo desde la interfaz de Home Assistant", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func jsonError(w http.ResponseWriter, code int, msg string, extra map[string]interface{}) {
	out := map[string]interface{}{"error": msg}
	for k, v := range extra {
		out[k] = v
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(out)
}

// "2026-11-14T20:30", "2026-11-14 20:30", RFC3339 o vacío (= ahora)
func parseCuando(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "ahora") {
		return time.Now(), nil
	}
	for _, l := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("fecha/hora no válida: %q", s)
}

// Destino EXACTO: JID, número de teléfono o nombre idéntico (sin ambigüedades).
func (a *app) resolveExact(to string) (types.JID, string, []chatRow, error) {
	to = strings.TrimSpace(to)
	if strings.Contains(to, "@") {
		j, err := types.ParseJID(to)
		if err != nil {
			return types.JID{}, "", nil, err
		}
		return j, firstNonEmpty(a.chatName(j), j.User), nil, nil
	}
	compact := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(to)
	international := strings.HasPrefix(compact, "+") || strings.HasPrefix(compact, "00")
	digits := strings.TrimPrefix(strings.TrimPrefix(compact, "+"), "00")
	if _, err := strconv.ParseUint(digits, 10, 64); err == nil && len(digits) >= 8 {
		if !international && len(digits) <= 10 { // número nacional: se añade el prefijo del país
			digits = a.opt.CountryCode + strings.TrimLeft(digits, "0")
		}
		j := types.NewJID(digits, types.DefaultUserServer)
		return j, firstNonEmpty(a.chatName(j), "+"+digits), nil, nil
	}
	cands := a.listChats(to, 10)
	var exact []chatRow
	for _, c := range cands {
		if strings.EqualFold(strings.TrimSpace(c.Nombre), to) {
			exact = append(exact, c)
		}
	}
	if len(exact) == 1 {
		j, _ := types.ParseJID(exact[0].JID)
		return j, exact[0].Nombre, nil, nil
	}
	return types.JID{}, "", cands, fmt.Errorf("destino ambiguo o desconocido: usa el JID o el nombre exacto")
}

func (a *app) inWhitelist(j types.JID, name string) bool {
	for _, x := range a.opt.AllowedSend {
		x = strings.TrimSpace(x)
		if x == "" {
			continue
		}
		num := strings.TrimPrefix(strings.ReplaceAll(x, " ", ""), "+")
		if strings.EqualFold(x, name) || x == j.String() || num == j.User ||
			(a.opt.CountryCode+strings.TrimLeft(num, "0")) == j.User {
			return true
		}
	}
	return false
}

func (a *app) insertProgramado(j types.JID, name, texto string, cuando time.Time, origen, estado string) (int64, error) {
	res, err := a.db.Exec(`INSERT INTO programados (msg_id, chat, chat_nombre, texto, cuando, origen, estado, creado)
		VALUES (?,?,?,?,?,?,?,?)`, string(a.cli.GenerateMessageID()), j.String(), name, texto, cuando.Unix(), origen, estado, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

/* ---------- desde la pantalla (el usuario, vía Ingress): sin restricciones ---------- */
func (a *app) handleUIProgramar(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Chat   string `json:"chat"`
		Texto  string `json:"texto"`
		Cuando string `json:"cuando"`
	}
	if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&req) != nil || strings.TrimSpace(req.Texto) == "" {
		jsonError(w, 400, "faltan datos", nil)
		return
	}
	j, err := types.ParseJID(req.Chat)
	if err != nil {
		jsonError(w, 400, "chat no válido", nil)
		return
	}
	t, err := parseCuando(req.Cuando)
	if err != nil {
		jsonError(w, 400, err.Error(), nil)
		return
	}
	if t.Before(time.Now().Add(-2 * time.Minute)) {
		jsonError(w, 400, "esa hora ya ha pasado", nil)
		return
	}
	id, err := a.insertProgramado(j, a.chatName(j), req.Texto, t, "pantalla", "pendiente")
	if err != nil {
		jsonError(w, 500, err.Error(), nil)
		return
	}
	go a.tick()
	writeJSON(w, map[string]interface{}{"ok": true, "id": id, "cuando": t.Format(fmtHora)})
}

/* ---------- API (asistentes / automatizaciones) ---------- */
func (a *app) handleAPIProgramar(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, 405, "usa POST", nil)
		return
	}
	var req struct {
		To     string `json:"to"`
		Text   string `json:"text"`
		When   string `json:"when"`
		Origen string `json:"origen"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || strings.TrimSpace(req.To) == "" || strings.TrimSpace(req.Text) == "" {
		jsonError(w, 400, "faltan to y text", nil)
		return
	}
	j, name, cands, err := a.resolveExact(req.To)
	if err != nil {
		jsonError(w, 409, err.Error(), map[string]interface{}{"candidatos": cands})
		return
	}
	t, err := parseCuando(req.When)
	if err != nil {
		jsonError(w, 400, err.Error(), nil)
		return
	}
	origen := "api"
	if req.Origen == "auto" {
		origen = "auto"
	}
	estado := "por_confirmar"
	if origen == "auto" && a.inWhitelist(j, name) {
		estado = "pendiente"
	}
	id, err := a.insertProgramado(j, name, req.Text, t, origen, estado)
	if err != nil {
		jsonError(w, 500, err.Error(), nil)
		return
	}
	if estado == "por_confirmar" {
		go a.askConfirm(id)
	} else {
		go a.tick()
	}
	writeJSON(w, map[string]interface{}{"ok": true, "id": id, "estado": estado, "a": name, "cuando": t.Format(fmtHora)})
}

func (a *app) getProgramado(id int64) (*programado, error) {
	var p programado
	var env sql.NullInt64
	var errS sql.NullString
	err := a.db.QueryRow(`SELECT id, chat, COALESCE(chat_nombre,''), texto, cuando, origen, estado, enviado, error FROM programados WHERE id = ?`, id).
		Scan(&p.ID, &p.Chat, &p.ChatNombre, &p.Texto, &p.CuandoTS, &p.Origen, &p.Estado, &env, &errS)
	if err != nil {
		return nil, err
	}
	p.Cuando = time.Unix(p.CuandoTS, 0).Format(fmtHora)
	if env.Valid && env.Int64 > 0 {
		p.Enviado = time.Unix(env.Int64, 0).Format(fmtHora)
	}
	p.Error = errS.String
	return &p, nil
}

func (a *app) listProgramados(soloActivos bool) []programado {
	q := `SELECT id FROM programados WHERE estado IN ('pendiente','por_confirmar','enviando') OR creado > ? ORDER BY
		CASE WHEN estado IN ('pendiente','por_confirmar','enviando') THEN 0 ELSE 1 END, cuando ASC LIMIT 100`
	since := time.Now().Add(-7 * 24 * time.Hour).Unix()
	if soloActivos {
		since = time.Now().Unix() + 1
	}
	rows, err := a.db.Query(q, since)
	if err != nil {
		return nil
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	out := []programado{}
	for _, id := range ids {
		if p, err := a.getProgramado(id); err == nil {
			out = append(out, *p)
		}
	}
	return out
}

func (a *app) countPending() int {
	var n int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM programados WHERE estado IN ('pendiente','por_confirmar')`).Scan(&n)
	return n
}

func (a *app) handleListar(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, a.listProgramados(r.URL.Query().Get("activos") == "1"))
}

func idFromReq(r *http.Request) int64 {
	var req struct {
		ID int64 `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	return req.ID
}

func (a *app) handleCancelar(w http.ResponseWriter, r *http.Request) {
	id := idFromReq(r)
	res, _ := a.db.Exec(`UPDATE programados SET estado = 'cancelado' WHERE id = ? AND estado IN ('pendiente','por_confirmar')`, id)
	n, _ := res.RowsAffected()
	writeJSON(w, map[string]interface{}{"ok": n == 1})
}

// Confirmación desde la pantalla (solo Ingress, es decir, el usuario con su sesión de Home Assistant)
func (a *app) handleConfirmar(w http.ResponseWriter, r *http.Request) {
	ok := a.confirmar(idFromReq(r))
	writeJSON(w, map[string]interface{}{"ok": ok})
}

func (a *app) confirmar(id int64) bool {
	res, _ := a.db.Exec(`UPDATE programados SET estado = 'pendiente', creado = ? WHERE id = ? AND estado = 'por_confirmar'`, time.Now().Unix(), id)
	n, _ := res.RowsAffected()
	if n == 1 {
		log.Printf("Programado %d confirmado", id)
		go a.tick()
	}
	return n == 1
}

func (a *app) handleContactos(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	out := []map[string]string{}
	if len(q) < 2 {
		writeJSON(w, out)
		return
	}
	all, err := a.cli.Store.Contacts.GetAllContacts(a.ctx)
	if err == nil {
		for j, c := range all {
			name := firstNonEmpty(c.FullName, c.FirstName, c.PushName, c.BusinessName)
			if name == "" || j.Server != types.DefaultUserServer {
				continue
			}
			if strings.Contains(strings.ToLower(name), q) || strings.Contains(j.User, q) {
				out = append(out, map[string]string{"jid": j.String(), "nombre": name})
				if len(out) >= 30 {
					break
				}
			}
		}
	}
	writeJSON(w, out)
}

/* ---------- el que envía ---------- */

var tickCh = make(chan struct{}, 1)

func (a *app) tick() {
	select {
	case tickCh <- struct{}{}:
	default:
	}
}

func (a *app) schedulerLoop() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-t.C:
		case <-tickCh:
		}
		a.processDue()
	}
}

func (a *app) processDue() {
	now := time.Now()
	rows, err := a.db.Query(`SELECT id FROM programados WHERE estado IN ('pendiente','por_confirmar') AND cuando <= ? ORDER BY cuando`, now.Unix())
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	late := time.Duration(a.opt.LateMinutes) * time.Minute
	for _, id := range ids {
		p, err := a.getProgramado(id)
		if err != nil {
			continue
		}
		dest := firstNonEmpty(p.ChatNombre, p.Chat)
		retraso := now.Sub(time.Unix(p.CuandoTS, 0))
		if p.Estado == "por_confirmar" {
			if retraso > late {
				a.setEstado(id, "caducado", "no se confirmó a tiempo")
				a.notify("⏰ WhatsApp sin enviar", fmt.Sprintf("No confirmaste el mensaje a %s y no se ha enviado:\n«%s»", dest, recorta(p.Texto, 120)), nil)
			}
			continue // espera a que el usuario confirme
		}
		if retraso > late {
			a.setEstado(id, "caducado", fmt.Sprintf("llevaba %s de retraso", retraso.Round(time.Minute)))
			a.notify("⚠️ WhatsApp sin enviar", fmt.Sprintf("El mensaje a %s era para las %s y llevaba %s de retraso, así que no lo he enviado:\n«%s»",
				dest, time.Unix(p.CuandoTS, 0).Format("15:04"), retraso.Round(time.Minute), recorta(p.Texto, 120)), nil)
			continue
		}
		// ¿El usuario ya ha escrito en ese chat desde que se programó? Entonces preguntar antes.
		// No se aplica a los envíos automáticos de la lista blanca (origen "auto"): son series que se
		// repiten en chats activos y cada aviso enviado ya cuenta como mensaje propio.
		if p.Origen != "auto" {
			var creado int64
			_ = a.db.QueryRow(`SELECT creado FROM programados WHERE id = ?`, id).Scan(&creado)
			var n int
			_ = a.db.QueryRow(`SELECT COUNT(*) FROM mensajes WHERE chat = ? AND de_mi = 1 AND ts > ?`, p.Chat, creado).Scan(&n)
			if n > 0 {
				_, _ = a.db.Exec(`UPDATE programados SET estado = 'por_confirmar', creado = ? WHERE id = ?`, now.Unix(), id)
				a.askConfirmText(id, fmt.Sprintf("Ya le has escrito a %s desde que programaste este mensaje. ¿Lo envío igualmente?\n«%s»", dest, recorta(p.Texto, 200)))
				continue
			}
		}
		var hoy int
		_ = a.db.QueryRow(`SELECT COUNT(*) FROM programados WHERE estado = 'enviado' AND enviado >= ?`,
			time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).Unix()).Scan(&hoy)
		if hoy >= a.opt.MaxPerDay {
			a.setEstado(id, "fallido", "límite diario de envíos alcanzado")
			a.notify("⚠️ WhatsApp sin enviar", fmt.Sprintf("Límite de %d envíos al día alcanzado; no se envió el mensaje a %s.", a.opt.MaxPerDay, dest), nil)
			continue
		}
		a.enviar(p)
	}
}

func recorta(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func (a *app) setEstado(id int64, estado, motivo string) {
	_, _ = a.db.Exec(`UPDATE programados SET estado = ?, error = ? WHERE id = ?`, estado, motivo, id)
}

func (a *app) enviar(p *programado) {
	j, err := types.ParseJID(p.Chat)
	if err != nil {
		a.setEstado(p.ID, "fallido", "destino no válido")
		return
	}
	dest := firstNonEmpty(p.ChatNombre, p.Chat)
	if !a.cli.IsConnected() || !a.cli.IsLoggedIn() {
		var intentos int
		_ = a.db.QueryRow(`SELECT intentos FROM programados WHERE id = ?`, p.ID).Scan(&intentos)
		_, _ = a.db.Exec(`UPDATE programados SET intentos = intentos + 1 WHERE id = ?`, p.ID)
		if intentos == 0 {
			log.Printf("Programado %d: WhatsApp desconectado, reintentando", p.ID)
		}
		return // se reintenta en el siguiente ciclo hasta que caduque
	}
	_, _ = a.db.Exec(`UPDATE programados SET estado = 'enviando' WHERE id = ?`, p.ID)
	var msgID string
	_ = a.db.QueryRow(`SELECT msg_id FROM programados WHERE id = ?`, p.ID).Scan(&msgID)

	// Como una persona: "escribiendo..." unos segundos según la longitud
	_ = a.cli.SendChatPresence(a.ctx, j, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	wait := time.Duration(2000+40*len([]rune(p.Texto))) * time.Millisecond
	if wait > 9*time.Second {
		wait = 9 * time.Second
	}
	time.Sleep(wait)
	_ = a.cli.SendChatPresence(a.ctx, j, types.ChatPresencePaused, types.ChatPresenceMediaText)

	texto := p.Texto
	_, err = a.cli.SendMessage(a.ctx, j, &waE2E.Message{Conversation: &texto}, whatsmeow.SendRequestExtra{ID: types.MessageID(msgID)})
	if err != nil {
		_, _ = a.db.Exec(`UPDATE programados SET estado = 'pendiente', intentos = intentos + 1, error = ? WHERE id = ?`, err.Error(), p.ID)
		var intentos int
		_ = a.db.QueryRow(`SELECT intentos FROM programados WHERE id = ?`, p.ID).Scan(&intentos)
		if intentos >= 5 {
			a.setEstado(p.ID, "fallido", err.Error())
			a.notify("⚠️ WhatsApp no enviado", fmt.Sprintf("No pude enviar el mensaje a %s: %s", dest, err.Error()), nil)
		}
		return
	}
	_, _ = a.db.Exec(`UPDATE programados SET estado = 'enviado', enviado = ?, error = NULL WHERE id = ?`, time.Now().Unix(), p.ID)
	log.Printf("Programado %d enviado a %s", p.ID, dest)
	a.notify("✅ WhatsApp enviado", fmt.Sprintf("A %s: «%s»", dest, recorta(p.Texto, 160)), nil)
}

/* ---------- Home Assistant: avisos al móvil y respuesta de los botones ---------- */

func supervisorToken() string { return os.Getenv("SUPERVISOR_TOKEN") }

func (a *app) notify(title, message string, data map[string]interface{}) {
	tok := supervisorToken()
	svc := strings.SplitN(strings.TrimPrefix(a.opt.NotifyService, "notify."), ".", 2)[0]
	if tok == "" || svc == "" {
		log.Println("Aviso (sin Home Assistant):", title, message)
		return
	}
	if data == nil {
		data = map[string]interface{}{}
	}
	data["channel"] = "WhatsApp"
	body, _ := json.Marshal(map[string]interface{}{"title": title, "message": message, "data": data})
	req, _ := http.NewRequestWithContext(a.ctx, http.MethodPost, "http://supervisor/core/api/services/notify/"+svc, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println("no pude avisar al móvil:", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Println("no pude avisar al móvil: HTTP", resp.StatusCode)
	}
}

func (a *app) askConfirm(id int64) {
	p, err := a.getProgramado(id)
	if err != nil {
		return
	}
	cuando := "ahora"
	if time.Unix(p.CuandoTS, 0).After(time.Now().Add(time.Minute)) {
		cuando = "el " + p.Cuando
	}
	a.askConfirmText(id, fmt.Sprintf("Un asistente quiere enviar a %s %s:\n«%s»", firstNonEmpty(p.ChatNombre, p.Chat), cuando, recorta(p.Texto, 300)))
}

func (a *app) askConfirmText(id int64, msg string) {
	a.notify("💬 ¿Envío este WhatsApp?", msg, map[string]interface{}{
		"tag":        fmt.Sprintf("wa-prog-%d", id),
		"importance": "high",
		"actions": []map[string]string{
			{"action": fmt.Sprintf("WA_OK_%d", id), "title": "Enviar"},
			{"action": fmt.Sprintf("WA_NO_%d", id), "title": "Cancelar"},
		},
	})
}

// Escucha los botones de las notificaciones (evento mobile_app_notification_action)
func (a *app) haEventsLoop() {
	tok := supervisorToken()
	if tok == "" {
		log.Println("Sin acceso a Home Assistant: las confirmaciones solo se podrán hacer desde la pantalla")
		return
	}
	for a.ctx.Err() == nil {
		if err := a.haEvents(tok); err != nil && a.ctx.Err() == nil {
			log.Println("conexión con Home Assistant:", err)
		}
		time.Sleep(10 * time.Second)
	}
}

func (a *app) haEvents(tok string) error {
	ctx, cancel := context.WithCancel(a.ctx)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws://supervisor/core/websocket", nil)
	if err != nil {
		return err
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	c.SetReadLimit(4 << 20)
	var msg map[string]interface{}
	if err := wsjson.Read(ctx, c, &msg); err != nil { // auth_required
		return err
	}
	if err := wsjson.Write(ctx, c, map[string]string{"type": "auth", "access_token": tok}); err != nil {
		return err
	}
	if err := wsjson.Read(ctx, c, &msg); err != nil || msg["type"] != "auth_ok" {
		return fmt.Errorf("autenticación rechazada: %v", msg["type"])
	}
	if err := wsjson.Write(ctx, c, map[string]interface{}{"id": 1, "type": "subscribe_events", "event_type": "mobile_app_notification_action"}); err != nil {
		return err
	}
	log.Println("Escuchando los botones de las notificaciones")
	for {
		var ev struct {
			Type  string `json:"type"`
			Event struct {
				Data map[string]interface{} `json:"data"`
			} `json:"event"`
		}
		if err := wsjson.Read(ctx, c, &ev); err != nil {
			return err
		}
		if ev.Type != "event" {
			continue
		}
		action, _ := ev.Event.Data["action"].(string)
		switch {
		case strings.HasPrefix(action, "WA_OK_"):
			if id, err := strconv.ParseInt(strings.TrimPrefix(action, "WA_OK_"), 10, 64); err == nil {
				if a.confirmar(id) {
					a.notify("👍 Confirmado", "Lo envío a su hora.", map[string]interface{}{"tag": fmt.Sprintf("wa-prog-%d", id)})
				}
			}
		case strings.HasPrefix(action, "WA_NO_"):
			if id, err := strconv.ParseInt(strings.TrimPrefix(action, "WA_NO_"), 10, 64); err == nil {
				_, _ = a.db.Exec(`UPDATE programados SET estado = 'cancelado' WHERE id = ? AND estado IN ('pendiente','por_confirmar')`, id)
				a.notify("🗑️ Cancelado", "No se enviará.", map[string]interface{}{"tag": fmt.Sprintf("wa-prog-%d", id)})
			}
		}
	}
}
