// WhatsApp puente — guarda el texto de tus conversaciones de WhatsApp en SQLite
// y ofrece una pequeña API local para Home Assistant (leer, buscar y, si se activa, enviar).
// Fotos, vídeos y audios no se descargan: solo se apunta que los hubo (y su pie de foto).
package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

const dataDir = "/data"

type options struct {
	APIToken    string   `json:"api_token"`
	SendEnabled bool     `json:"send_enabled"`
	AllowedSend []string `json:"allowed_send"`
}

type app struct {
	opt    options
	db     *sql.DB
	cli    *whatsmeow.Client
	ctx    context.Context
	mu     sync.Mutex
	qr     string // código QR pendiente de escanear ("" si no hay)
	status string
	names  sync.Map // jid -> nombre de chat/grupo/contacto
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	a := &app{ctx: ctx, status: "arrancando"}
	a.loadOptions()

	var err error
	a.db, err = sql.Open("sqlite3", "file:"+dataDir+"/mensajes.db?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		log.Fatalf("base de datos de mensajes: %v", err)
	}
	a.db.SetMaxOpenConns(1)
	a.initSchema()

	dbLog := waLog.Stdout("Sesion", "WARN", true)
	container, err := sqlstore.New(ctx, "sqlite3", "file:"+dataDir+"/sesion.db?_foreign_keys=on&_journal_mode=WAL", dbLog)
	if err != nil {
		log.Fatalf("base de datos de sesión: %v", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		log.Fatalf("dispositivo: %v", err)
	}
	a.cli = whatsmeow.NewClient(device, waLog.Stdout("WhatsApp", "INFO", true))
	a.cli.AddEventHandler(a.onEvent)

	go a.serveHTTP()
	go a.connectLoop()

	<-ctx.Done()
	a.cli.Disconnect()
	a.db.Close()
}

func (a *app) loadOptions() {
	b, err := os.ReadFile(dataDir + "/options.json")
	if err == nil {
		_ = json.Unmarshal(b, &a.opt)
	}
	if a.opt.APIToken == "" {
		log.Println("AVISO: api_token vacío; la API solo responderá a la interfaz de Home Assistant (Ingress).")
	}
}

/* ---------------- conexión y QR ---------------- */

func (a *app) setStatus(s string) {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
	log.Println("Estado:", s)
}

func (a *app) connectLoop() {
	for a.ctx.Err() == nil {
		if a.cli.Store.ID == nil {
			// Sin vincular: pedimos QR y esperamos a que se escanee
			qrChan, _ := a.cli.GetQRChannel(a.ctx)
			if err := a.cli.Connect(); err != nil {
				a.setStatus("error al conectar: " + err.Error())
				time.Sleep(30 * time.Second)
				continue
			}
			a.setStatus("esperando QR")
			for evt := range qrChan {
				switch evt.Event {
				case "code":
					a.mu.Lock()
					a.qr = evt.Code
					a.mu.Unlock()
				case "success":
					a.mu.Lock()
					a.qr = ""
					a.mu.Unlock()
					a.setStatus("vinculado")
				default: // timeout, error...
					a.mu.Lock()
					a.qr = ""
					a.mu.Unlock()
					a.setStatus("QR caducado (" + evt.Event + "), se genera otro")
				}
			}
			if a.cli.Store.ID == nil {
				a.cli.Disconnect()
				time.Sleep(3 * time.Second)
				continue
			}
		} else if !a.cli.IsConnected() {
			if err := a.cli.Connect(); err != nil {
				a.setStatus("error al conectar: " + err.Error())
				time.Sleep(30 * time.Second)
				continue
			}
		}
		// Ya conectado: whatsmeow reconecta solo; vigilamos por si se cierra la sesión
		for a.ctx.Err() == nil && a.cli.Store.ID != nil {
			time.Sleep(15 * time.Second)
		}
	}
}

/* ---------------- eventos ---------------- */

func (a *app) onEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Connected:
		a.setStatus("conectado")
		a.scheduleFix()
	case *events.Disconnected:
		a.setStatus("desconectado (reintentando)")
	case *events.LoggedOut:
		a.setStatus("sesión cerrada desde el móvil: hay que volver a vincular")
	case *events.Message:
		a.saveMessage(v, false)
	case *events.HistorySync:
		a.saveHistory(v)
	}
}

var fixTimer *time.Timer
var fixMu sync.Mutex

// Repasa nombres 60 s después del último aviso (los historiales llegan a trozos)
func (a *app) scheduleFix() {
	fixMu.Lock()
	defer fixMu.Unlock()
	if fixTimer != nil {
		fixTimer.Stop()
	}
	fixTimer = time.AfterFunc(60*time.Second, func() { a.fixNames() })
}

func textOf(m *waE2E.Message) (text, kind string) {
	if m == nil {
		return "", ""
	}
	switch {
	case m.GetConversation() != "":
		return m.GetConversation(), "texto"
	case m.GetExtendedTextMessage() != nil:
		return m.GetExtendedTextMessage().GetText(), "texto"
	case m.GetImageMessage() != nil:
		return strings.TrimSpace("[foto] " + m.GetImageMessage().GetCaption()), "foto"
	case m.GetVideoMessage() != nil:
		return strings.TrimSpace("[vídeo] " + m.GetVideoMessage().GetCaption()), "video"
	case m.GetAudioMessage() != nil:
		return "[audio]", "audio"
	case m.GetDocumentMessage() != nil:
		d := m.GetDocumentMessage()
		return strings.TrimSpace("[documento] " + d.GetFileName() + " " + d.GetCaption()), "documento"
	case m.GetStickerMessage() != nil:
		return "[sticker]", "sticker"
	case m.GetLocationMessage() != nil:
		return "[ubicación] " + m.GetLocationMessage().GetName(), "ubicacion"
	case m.GetContactMessage() != nil:
		return "[contacto] " + m.GetContactMessage().GetDisplayName(), "contacto"
	case m.GetPollCreationMessage() != nil:
		return "[encuesta] " + m.GetPollCreationMessage().GetName(), "encuesta"
	case m.GetPollCreationMessageV3() != nil:
		return "[encuesta] " + m.GetPollCreationMessageV3().GetName(), "encuesta"
	}
	return "", ""
}

func (a *app) chatName(jid types.JID) string {
	key := jid.String()
	if v, ok := a.names.Load(key); ok {
		return v.(string)
	}
	name := ""
	if jid.Server == types.GroupServer {
		if gi, err := a.cli.GetGroupInfo(a.ctx, jid); err == nil && gi != nil {
			name = gi.Name
		}
	} else {
		name = a.personName(jid)
	}
	if name != "" {
		a.names.Store(key, name)
	}
	return name
}

func (a *app) contactName(j types.JID) string {
	if c, err := a.cli.Store.Contacts.GetContact(a.ctx, j); err == nil && c.Found {
		return firstNonEmpty(c.FullName, c.FirstName, c.PushName, c.BusinessName)
	}
	return ""
}

func (a *app) storedName(j types.JID) string {
	var n string
	_ = a.db.QueryRow(`SELECT nombre FROM nombres WHERE jid = ?`, j.ToNonAD().String()).Scan(&n)
	return n
}

// Nombre de una persona: agenda del móvil, nombre de perfil visto, y si es un identificador
// interno de WhatsApp (LID, "@lid") se traduce a su número de teléfono primero.
func (a *app) personName(j types.JID) string {
	j = j.ToNonAD()
	if n := firstNonEmpty(a.contactName(j), a.storedName(j)); n != "" {
		return n
	}
	if j.Server == types.HiddenUserServer {
		if pn, err := a.cli.Store.LIDs.GetPNForLID(a.ctx, j); err == nil && !pn.IsEmpty() {
			if n := firstNonEmpty(a.contactName(pn), a.storedName(pn)); n != "" {
				return n
			}
			return "+" + pn.User
		}
	}
	return ""
}

func (a *app) rememberName(j types.JID, name string) {
	name = strings.TrimSpace(name)
	if name == "" || j.IsEmpty() {
		return
	}
	_, _ = a.db.Exec(`INSERT INTO nombres (jid, nombre) VALUES (?, ?)
		ON CONFLICT(jid) DO UPDATE SET nombre = excluded.nombre`, j.ToNonAD().String(), name)
}

// Rellena los remitentes sin nombre de mensajes ya guardados
func (a *app) fixNames() int {
	rows, err := a.db.Query(`SELECT DISTINCT remitente FROM mensajes
		WHERE de_mi = 0 AND (nombre_remitente IS NULL OR nombre_remitente = '' OR nombre_remitente LIKE '+%')`)
	if err != nil {
		return 0
	}
	var jids []string
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			jids = append(jids, s)
		}
	}
	rows.Close()
	n := 0
	for _, s := range jids {
		j, err := types.ParseJID(s)
		if err != nil {
			continue
		}
		a.names.Delete(j.ToNonAD().String())
		if name := a.personName(j); name != "" {
			if res, err := a.db.Exec(`UPDATE mensajes SET nombre_remitente = ? WHERE remitente = ? AND de_mi = 0
				AND (nombre_remitente IS NULL OR nombre_remitente = '' OR nombre_remitente LIKE '+%')`, name, s); err == nil {
				c, _ := res.RowsAffected()
				n += int(c)
			}
		}
	}
	log.Printf("Nombres recuperados en %d mensajes (%d remitentes revisados)", n, len(jids))
	return n
}

var mentionRe = regexp.MustCompile(`@(\d{6,})`)

// Cambia "@123456789" (menciones) por "@Nombre" cuando se sabe quién es
func (a *app) withMentions(t string) string {
	return mentionRe.ReplaceAllStringFunc(t, func(m string) string {
		num := m[1:]
		for _, srv := range []string{types.HiddenUserServer, types.DefaultUserServer} {
			if n := a.personName(types.NewJID(num, srv)); n != "" && !strings.HasPrefix(n, "+") {
				return "@" + n
			}
		}
		return m
	})
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

func (a *app) saveMessage(evt *events.Message, fromHistory bool) {
	text, kind := textOf(evt.Message)
	if text == "" {
		return // reacciones, cambios de grupo, mensajes del sistema...
	}
	info := evt.Info
	if info.Chat.Server == types.BroadcastServer {
		return // estados
	}
	chat := info.Chat.ToNonAD()
	sender := info.Sender.ToNonAD()
	if !info.IsFromMe && info.PushName != "" {
		a.rememberName(sender, info.PushName)
	}
	senderName := ""
	if info.IsFromMe {
		senderName = "Yo"
	} else {
		senderName = firstNonEmpty(a.personName(sender), info.PushName)
	}
	chatName := a.chatName(chat)
	if chatName == "" && chat.Server != types.GroupServer && !info.IsFromMe {
		chatName = info.PushName
	}
	isGroup := chat.Server == types.GroupServer

	_, err := a.db.Exec(`INSERT INTO mensajes (id, chat, remitente, nombre_remitente, de_mi, ts, texto, tipo)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(id, chat) DO UPDATE SET texto=excluded.texto`,
		info.ID, chat.String(), sender.String(), senderName, boolInt(info.IsFromMe), info.Timestamp.Unix(), text, kind)
	if err != nil {
		log.Println("guardar mensaje:", err)
		return
	}
	_, _ = a.db.Exec(`INSERT INTO chats (jid, nombre, grupo, ultimo_ts) VALUES (?,?,?,?)
		ON CONFLICT(jid) DO UPDATE SET
		  nombre = CASE WHEN excluded.nombre <> '' THEN excluded.nombre ELSE chats.nombre END,
		  ultimo_ts = MAX(chats.ultimo_ts, excluded.ultimo_ts)`,
		chat.String(), chatName, boolInt(isGroup), info.Timestamp.Unix())
}

func (a *app) saveHistory(h *events.HistorySync) {
	for _, pn := range h.Data.GetPushnames() {
		if j, err := types.ParseJID(pn.GetID()); err == nil {
			a.rememberName(j, pn.GetPushname())
		}
	}
	n := 0
	for _, conv := range h.Data.GetConversations() {
		chatJID, err := types.ParseJID(conv.GetID())
		if err != nil {
			continue
		}
		if name := conv.GetName(); name != "" {
			a.names.Store(chatJID.ToNonAD().String(), name)
		}
		for _, hm := range conv.GetMessages() {
			evt, err := a.cli.ParseWebMessage(chatJID, hm.GetMessage())
			if err != nil {
				continue
			}
			a.saveMessage(evt, true)
			n++
		}
	}
	log.Printf("Historial recibido: %d mensajes revisados", n)
	a.scheduleFix()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (a *app) initSchema() {
	_, err := a.db.Exec(`
CREATE TABLE IF NOT EXISTS mensajes (
  id TEXT NOT NULL, chat TEXT NOT NULL, remitente TEXT, nombre_remitente TEXT,
  de_mi INTEGER, ts INTEGER, texto TEXT, tipo TEXT,
  PRIMARY KEY (id, chat));
CREATE INDEX IF NOT EXISTS idx_mensajes_chat_ts ON mensajes(chat, ts);
CREATE INDEX IF NOT EXISTS idx_mensajes_ts ON mensajes(ts);
CREATE TABLE IF NOT EXISTS nombres (jid TEXT PRIMARY KEY, nombre TEXT);
CREATE TABLE IF NOT EXISTS chats (
  jid TEXT PRIMARY KEY, nombre TEXT, grupo INTEGER, ultimo_ts INTEGER);`)
	if err != nil {
		log.Fatalf("esquema: %v", err)
	}
}

/* ---------------- API HTTP ---------------- */

// La interfaz de Home Assistant (Ingress) llega siempre desde 172.30.32.2
func fromIngress(r *http.Request) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host == "172.30.32.2"
}

func (a *app) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if fromIngress(r) {
			next(w, r)
			return
		}
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if a.opt.APIToken == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(a.opt.APIToken)) != 1 {
			http.Error(w, "no autorizado", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func (a *app) serveHTTP() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.auth(a.handleIndex))
	mux.HandleFunc("/qr.png", a.auth(a.handleQR))
	mux.HandleFunc("/api/status", a.auth(a.handleStatus))
	mux.HandleFunc("/api/chats", a.auth(a.handleChats))
	mux.HandleFunc("/api/messages", a.auth(a.handleMessages))
	mux.HandleFunc("/api/search", a.auth(a.handleSearch))
	mux.HandleFunc("/api/send", a.auth(a.handleSend))
	mux.HandleFunc("/api/fix-names", a.auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]int{"actualizados": a.fixNames()})
	}))
	log.Println("API escuchando en :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

func (a *app) stats() map[string]interface{} {
	var total, chats int64
	var last sql.NullInt64
	_ = a.db.QueryRow(`SELECT COUNT(*), MAX(ts) FROM mensajes`).Scan(&total, &last)
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM chats`).Scan(&chats)
	a.mu.Lock()
	st, qr := a.status, a.qr
	a.mu.Unlock()
	out := map[string]interface{}{
		"estado":              st,
		"conectado":           a.cli.IsConnected(),
		"vinculado":           a.cli.Store.ID != nil,
		"esperando_qr":        qr != "",
		"mensajes":            total,
		"chats":               chats,
		"envio_activo":        a.opt.SendEnabled,
		"destinos_permitidos": len(a.opt.AllowedSend),
	}
	if a.cli.Store.ID != nil {
		out["cuenta"] = a.cli.Store.ID.User
		out["nombre"] = a.cli.Store.PushName
	}
	if last.Valid {
		out["ultimo_mensaje"] = time.Unix(last.Int64, 0).Local().Format(time.RFC3339)
	}
	return out
}

func (a *app) handleStatus(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.stats()) }

func (a *app) handleQR(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	code := a.qr
	a.mu.Unlock()
	if code == "" {
		http.NotFound(w, r)
		return
	}
	png, err := qrcode.Encode(code, qrcode.Medium, 360)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

type chatRow struct {
	JID      string `json:"jid"`
	Nombre   string `json:"nombre"`
	Grupo    bool   `json:"grupo"`
	Ultimo   string `json:"ultimo"`
	Mensajes int64  `json:"mensajes"`
}

func (a *app) listChats(q string, limit int) []chatRow {
	rows, err := a.db.Query(`SELECT c.jid, COALESCE(c.nombre,''), c.grupo, c.ultimo_ts,
		(SELECT COUNT(*) FROM mensajes m WHERE m.chat = c.jid)
		FROM chats c WHERE (? = '' OR c.nombre LIKE '%'||?||'%' OR c.jid LIKE '%'||?||'%')
		ORDER BY c.ultimo_ts DESC LIMIT ?`, q, q, q, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []chatRow{}
	for rows.Next() {
		var c chatRow
		var g int
		var ts int64
		if rows.Scan(&c.JID, &c.Nombre, &g, &ts, &c.Mensajes) == nil {
			c.Grupo = g == 1
			c.Ultimo = time.Unix(ts, 0).Local().Format("2006-01-02 15:04")
			out = append(out, c)
		}
	}
	return out
}

func (a *app) handleChats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, a.listChats(r.URL.Query().Get("q"), atoiDef(r.URL.Query().Get("limit"), 50)))
}

// since: "24h", "7d", "2026-11-01" o vacío (= últimas 24 h)
func parseSince(s string) int64 {
	now := time.Now()
	s = strings.TrimSpace(s)
	if s == "" {
		return now.Add(-24 * time.Hour).Unix()
	}
	if strings.HasSuffix(s, "h") || strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(s[:len(s)-1])
		if err == nil {
			d := time.Duration(n) * time.Hour
			if strings.HasSuffix(s, "d") {
				d *= 24
			}
			return now.Add(-d).Unix()
		}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t.Unix()
		}
	}
	return now.Add(-24 * time.Hour).Unix()
}

func atoiDef(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return def
}

type msgRow struct {
	Hora  string `json:"hora"`
	Chat  string `json:"chat,omitempty"`
	Quien string `json:"quien"`
	Texto string `json:"texto"`
}

// Busca el chat por JID exacto o por parte del nombre (el más reciente que coincida)
func (a *app) resolveChat(q string) (string, string) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", ""
	}
	var jid, name string
	err := a.db.QueryRow(`SELECT jid, COALESCE(nombre,'') FROM chats WHERE jid = ? OR nombre LIKE '%'||?||'%'
		ORDER BY (jid = ?) DESC, ultimo_ts DESC LIMIT 1`, q, q, q).Scan(&jid, &name)
	if err != nil {
		return "", ""
	}
	return jid, name
}

func (a *app) handleMessages(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	jid, name := a.resolveChat(qs.Get("chat"))
	if jid == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "chat no encontrado", "parecidos": a.listChats(qs.Get("chat"), 10)})
		return
	}
	since := parseSince(qs.Get("since"))
	limit := atoiDef(qs.Get("limit"), 500)
	rows, err := a.db.Query(`SELECT ts, nombre_remitente, texto FROM (
		SELECT ts, nombre_remitente, texto FROM mensajes WHERE chat = ? AND ts >= ? ORDER BY ts DESC LIMIT ?)
		ORDER BY ts ASC`, jid, since, limit)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	msgs := []msgRow{}
	for rows.Next() {
		var ts int64
		var m msgRow
		if rows.Scan(&ts, &m.Quien, &m.Texto) == nil {
			m.Hora = time.Unix(ts, 0).Local().Format("2006-01-02 15:04")
			msgs = append(msgs, m)
		}
	}
	rows.Close() // la base de datos tiene una sola conexión: soltarla antes de buscar nombres
	for i := range msgs {
		msgs[i].Texto = a.withMentions(msgs[i].Texto)
		if msgs[i].Quien == "" {
			msgs[i].Quien = "?"
		}
	}
	writeJSON(w, map[string]interface{}{"chat": name, "jid": jid, "desde": time.Unix(since, 0).Local().Format("2006-01-02 15:04"), "total": len(msgs), "mensajes": msgs})
}

func (a *app) handleSearch(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	q := strings.TrimSpace(qs.Get("q"))
	if q == "" {
		http.Error(w, "falta q", 400)
		return
	}
	since := parseSince(firstNonEmpty(qs.Get("since"), "365d"))
	rows, err := a.db.Query(`SELECT m.ts, COALESCE(c.nombre, m.chat), m.nombre_remitente, m.texto
		FROM mensajes m LEFT JOIN chats c ON c.jid = m.chat
		WHERE m.ts >= ? AND m.texto LIKE '%'||?||'%' ORDER BY m.ts DESC LIMIT ?`,
		since, q, atoiDef(qs.Get("limit"), 100))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []msgRow{}
	for rows.Next() {
		var ts int64
		var m msgRow
		if rows.Scan(&ts, &m.Chat, &m.Quien, &m.Texto) == nil {
			m.Hora = time.Unix(ts, 0).Local().Format("2006-01-02 15:04")
			out = append(out, m)
		}
	}
	writeJSON(w, out)
}

/* Envío: desactivado por defecto; solo a destinos de la lista blanca (JID, número o nombre exacto del chat) */
func (a *app) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "usa POST", 405)
		return
	}
	if !a.opt.SendEnabled {
		http.Error(w, "el envío está desactivado (send_enabled)", 403)
		return
	}
	var req struct {
		To   string `json:"to"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.To) == "" || strings.TrimSpace(req.Text) == "" {
		http.Error(w, "faltan to y text", 400)
		return
	}
	jidStr, name := a.resolveChat(req.To)
	if jidStr == "" && !strings.Contains(req.To, "@") {
		jidStr = strings.TrimPrefix(strings.ReplaceAll(req.To, " ", ""), "+") + "@" + types.DefaultUserServer
	} else if jidStr == "" {
		jidStr = req.To
	}
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		http.Error(w, "destino no válido", 400)
		return
	}
	allowed := false
	for _, x := range a.opt.AllowedSend {
		x = strings.TrimSpace(x)
		if x != "" && (strings.EqualFold(x, name) || x == jid.String() || strings.TrimPrefix(x, "+") == jid.User) {
			allowed = true
			break
		}
	}
	if !allowed {
		http.Error(w, "destino fuera de la lista blanca", 403)
		return
	}
	// Como una persona: "escribiendo..." unos segundos según la longitud
	_ = a.cli.SendChatPresence(a.ctx, jid, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	wait := time.Duration(2000+40*len([]rune(req.Text))) * time.Millisecond
	if wait > 9*time.Second {
		wait = 9 * time.Second
	}
	time.Sleep(wait)
	_ = a.cli.SendChatPresence(a.ctx, jid, types.ChatPresencePaused, types.ChatPresenceMediaText)
	resp, err := a.cli.SendMessage(a.ctx, jid, &waE2E.Message{Conversation: &req.Text})
	if err != nil {
		http.Error(w, "no se pudo enviar: "+err.Error(), 502)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "id": resp.ID, "a": firstNonEmpty(name, jid.String())})
}

/* ---------------- página (Ingress) ---------------- */

var page = template.Must(template.New("p").Parse(`<!doctype html><html lang="es"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>WhatsApp puente</title>
<style>
:root{color-scheme:light dark;--bg:#f6f7f9;--card:#fff;--txt:#111;--sub:#667;--ok:#16a34a;--warn:#d97706}
@media (prefers-color-scheme:dark){:root{--bg:#111318;--card:#1b1e25;--txt:#eee;--sub:#99a}}
body{margin:0;font:16px/1.4 system-ui,sans-serif;background:var(--bg);color:var(--txt)}
main{max-width:760px;margin:0 auto;padding:20px}
.card{background:var(--card);border-radius:14px;padding:18px 20px;margin-bottom:14px;box-shadow:0 1px 3px rgba(0,0,0,.08)}
h1{font-size:22px;margin:0 0 6px}.sub{color:var(--sub)}
.dot{display:inline-block;width:10px;height:10px;border-radius:50%;margin-right:6px;background:var(--warn)}
.dot.ok{background:var(--ok)}
.qr{text-align:center}.qr img{width:300px;height:300px;background:#fff;padding:10px;border-radius:12px}
table{width:100%;border-collapse:collapse}td{padding:6px 4px;border-bottom:1px solid rgba(128,128,128,.2)}
td.n{text-align:right;color:var(--sub);white-space:nowrap}
</style></head><body><main>
<div class="card"><h1>💬 WhatsApp puente</h1>
<div><span class="dot {{if .S.conectado}}ok{{end}}"></span><b>{{.S.estado}}</b>{{if .S.nombre}} · {{.S.nombre}}{{end}}</div>
<div class="sub">{{.S.mensajes}} mensajes guardados en {{.S.chats}} chats{{if .S.ultimo_mensaje}} · último {{.S.ultimo_mensaje}}{{end}} · envío {{if .S.envio_activo}}activado ({{.S.destinos_permitidos}} destinos){{else}}desactivado{{end}}</div></div>
{{if .S.esperando_qr}}<div class="card qr"><h1>Vincula tu WhatsApp</h1>
<p class="sub">En el móvil: WhatsApp → Ajustes → Dispositivos vinculados → Vincular dispositivo, y escanea este código.</p>
<img src="qr.png?t={{.T}}" alt="Código QR"></div>{{end}}
{{if .C}}<div class="card"><h1>Chats recientes</h1><table>
{{range .C}}<tr><td>{{if .Grupo}}👥 {{end}}{{if .Nombre}}{{.Nombre}}{{else}}{{.JID}}{{end}}</td><td class="n">{{.Mensajes}} · {{.Ultimo}}</td></tr>{{end}}
</table></div>{{end}}
<p class="sub">Solo se guarda el texto. Fotos, vídeos y audios no se descargan. La página se actualiza sola.</p>
</main><script>setTimeout(()=>location.reload(), {{if .S.esperando_qr}}4000{{else}}30000{{end}})</script></body></html>`))

func (a *app) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = page.Execute(w, map[string]interface{}{"S": a.stats(), "C": a.listChats("", 25), "T": time.Now().UnixNano()})
}

var _ = fmt.Sprintf
