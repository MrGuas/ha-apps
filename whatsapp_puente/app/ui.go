// Página de la App (se abre desde Home Assistant con Ingress): estado, QR, programados y chats.
package main

import (
	"html/template"
	"net/http"
	"time"
)

var page = template.Must(template.New("p").Parse(`<!doctype html><html lang="es"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>WhatsApp puente</title>
<style>
:root{color-scheme:light dark;--bg:#f3f5f7;--card:#fff;--txt:#111b21;--sub:#667781;--line:rgba(128,128,128,.22);--wa:#00a884;--warn:#d97706;--bad:#dc2626;--bub:#d9fdd3}
@media (prefers-color-scheme:dark){:root{--bg:#0b141a;--card:#111b21;--txt:#e9edef;--sub:#8696a0;--bub:#005c4b}}
*{box-sizing:border-box}
body{margin:0;font:16px/1.4 system-ui,-apple-system,sans-serif;background:var(--bg);color:var(--txt)}
main{max-width:760px;margin:0 auto;padding:16px}
.card{background:var(--card);border-radius:14px;padding:16px 18px;margin-bottom:14px;box-shadow:0 1px 3px rgba(0,0,0,.08)}
h1{font-size:20px;margin:0 0 6px}h2{font-size:17px;margin:0 0 10px}.sub{color:var(--sub);font-size:14px}
.dot{display:inline-block;width:10px;height:10px;border-radius:50%;margin-right:6px;background:var(--warn)}.dot.ok{background:var(--wa)}
.qr{text-align:center}.qr img{width:300px;max-width:100%;background:#fff;padding:10px;border-radius:12px}
input,textarea,button{font:inherit;color:inherit}
input[type=search],input[type=date],input[type=time],textarea{width:100%;padding:10px 12px;border-radius:10px;border:1px solid var(--line);background:transparent}
textarea{min-height:90px;resize:vertical}
.chat{display:flex;justify-content:space-between;gap:10px;padding:10px 4px;border-bottom:1px solid var(--line);cursor:pointer}
.chat:hover{background:rgba(128,128,128,.07)}.chat .n{color:var(--sub);font-size:13px;white-space:nowrap}
.form{padding:12px 4px 16px;border-bottom:1px solid var(--line)}
.row{display:flex;gap:10px;margin:10px 0}.row>*{flex:1}
.btns{display:flex;gap:10px;flex-wrap:wrap}
button{border:0;border-radius:999px;padding:10px 18px;font-weight:600;cursor:pointer;background:var(--wa);color:#fff}
button.sec{background:transparent;color:var(--txt);border:1px solid var(--line)}button.bad{background:var(--bad)}
.prev{margin:10px 0;display:flex;justify-content:flex-end}
.bubble{background:var(--bub);border-radius:10px 10px 2px 10px;padding:8px 10px;max-width:85%;white-space:pre-wrap;word-wrap:break-word;font-size:15px}
.p{display:flex;gap:10px;align-items:flex-start;padding:10px 0;border-bottom:1px solid var(--line)}
.p .t{flex:1;min-width:0}.p .m{white-space:pre-wrap;word-wrap:break-word;margin-top:2px}
.tag{display:inline-block;font-size:12px;font-weight:700;border-radius:999px;padding:1px 8px;margin-left:6px;background:rgba(128,128,128,.18)}
.tag.pendiente{background:rgba(0,168,132,.18);color:var(--wa)}.tag.por_confirmar{background:rgba(217,119,6,.18);color:var(--warn)}
.tag.fallido,.tag.caducado{background:rgba(220,38,38,.15);color:var(--bad)}
.msg{margin-top:8px;font-size:14px}.msg.err{color:var(--bad)}.msg.ok{color:var(--wa)}
.hide{display:none}
</style></head><body><main>
<div class="card"><h1>💬 WhatsApp puente</h1>
<div><span class="dot {{if .S.conectado}}ok{{end}}"></span><b>{{.S.estado}}</b>{{if .S.nombre}} · {{.S.nombre}}{{end}}</div>
<div class="sub">{{.S.mensajes}} mensajes en {{.S.chats}} chats · hora del servidor: {{.S.hora_servidor}}</div></div>

{{if .S.esperando_qr}}<div class="card qr"><h1>Vincula tu WhatsApp</h1>
<p class="sub">En el móvil: WhatsApp → Ajustes → Dispositivos vinculados → Vincular dispositivo, y escanea este código.</p>
<img src="qr.png?t={{.T}}" alt="Código QR"></div>{{end}}

<div class="card"><h2>🕒 Programados</h2>
{{if .P}}{{range .P}}<div class="p"><div class="t">
<b>{{if .ChatNombre}}{{.ChatNombre}}{{else}}{{.Chat}}{{end}}</b><span class="tag {{.Estado}}">{{if eq .Estado "por_confirmar"}}por confirmar{{else if eq .Estado "pendiente"}}programado{{else}}{{.Estado}}{{end}}</span>
<div class="sub">{{if .Enviado}}enviado {{.Enviado}}{{else}}para el {{.Cuando}}{{end}} · {{if eq .Origen "pantalla"}}programado por ti{{else if or (eq .Origen "api") (eq .Origen "claude")}}programado por un asistente{{else}}automático{{end}}{{if .Error}} · {{.Error}}{{end}}</div>
<div class="m">{{.Texto}}</div></div>
<div class="btns">{{if eq .Estado "por_confirmar"}}<button data-ok="{{.ID}}">Enviar</button>{{end}}
{{if or (eq .Estado "pendiente") (eq .Estado "por_confirmar")}}<button class="sec" data-no="{{.ID}}">Cancelar</button>{{end}}</div></div>{{end}}
{{else}}<div class="sub">No hay mensajes programados.</div>{{end}}</div>

<div class="card"><h2>✍️ Programar un mensaje</h2>
<input type="search" id="q" placeholder="Buscar chat o contacto…" autocomplete="off">
<div id="lista">{{range .C}}<div class="chat" data-jid="{{.JID}}" data-name="{{if .Nombre}}{{.Nombre}}{{else}}{{.JID}}{{end}}"><span>{{if .Grupo}}👥 {{end}}{{if .Nombre}}{{.Nombre}}{{else}}{{.JID}}{{end}}</span><span class="n">{{.Ultimo}}</span></div>{{end}}</div>
<div id="extra"></div>
</div>

<template id="tpl"><div class="form">
<textarea placeholder="Escribe el mensaje… (*negrita*, _cursiva_)"></textarea>
<div class="prev hide"><div class="bubble"></div></div>
<div class="row"><input type="date"><input type="time"></div>
<div class="btns"><button class="prog">Programar</button><button class="sec ahora">Enviar ahora</button><button class="sec cerrar">Cerrar</button></div>
<div class="msg"></div></div></template>

<p class="sub">Solo se guarda el texto. Lo que programas aquí sale tal cual; lo que programa un asistente por la API espera a que lo confirmes.</p>
</main>
<script>
const $=(s,e=document)=>e.querySelector(s);
const pad=n=>String(n).padStart(2,'0');
const esc=s=>s.replace(/[&<>]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;'}[c]));
const fmt=s=>esc(s).replace(/\*([^*\n]+)\*/g,'<b>$1</b>').replace(/_([^_\n]+)_/g,'<i>$1</i>').replace(/~([^~\n]+)~/g,'<s>$1</s>');
let abierto=null, editando=false;
async function post(url,body){const r=await fetch(url,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});const j=await r.json().catch(()=>({}));if(!r.ok)throw new Error(j.error||('HTTP '+r.status));return j}
function abrir(el){
  if(abierto){abierto.remove();if(abierto.previousElementSibling===el){abierto=null;editando=false;return}}
  const f=$('#tpl').content.firstElementChild.cloneNode(true); el.after(f); abierto=f; editando=true;
  const d=new Date(Date.now()+60*60*1000); d.setMinutes(Math.ceil(d.getMinutes()/5)*5,0,0);
  $('input[type=date]',f).value=d.getFullYear()+'-'+pad(d.getMonth()+1)+'-'+pad(d.getDate());
  $('input[type=time]',f).value=pad(d.getHours())+':'+pad(d.getMinutes());
  const ta=$('textarea',f); ta.focus();
  ta.addEventListener('input',()=>{$('.prev',f).classList.toggle('hide',!ta.value.trim());$('.bubble',f).innerHTML=fmt(ta.value)});
  const enviar=async(ahora)=>{
    const msg=$('.msg',f); msg.className='msg'; const texto=ta.value.trim();
    if(!texto){msg.textContent='Escribe el mensaje';msg.classList.add('err');return}
    let cuando='ahora';
    if(!ahora){cuando=$('input[type=date]',f).value+'T'+$('input[type=time]',f).value;const h=+$('input[type=time]',f).value.slice(0,2);
      if((h>=23||h<8)&&!confirm('Se enviará a las '+$('input[type=time]',f).value+', de madrugada. ¿Seguro?'))return}
    else if(!confirm('¿Enviar ahora a '+el.dataset.name+'?'))return;
    try{const j=await post('ui/programar',{chat:el.dataset.jid,texto,cuando});msg.textContent=ahora?'Enviando…':'Programado para el '+j.cuando;msg.classList.add('ok');editando=false;setTimeout(()=>location.reload(),1500)}
    catch(e){msg.textContent=e.message;msg.classList.add('err')}};
  $('.prog',f).onclick=()=>enviar(false); $('.ahora',f).onclick=()=>enviar(true);
  $('.cerrar',f).onclick=()=>{f.remove();abierto=null;editando=false};
}
document.addEventListener('click',async e=>{
  const c=e.target.closest('.chat'); if(c){abrir(c);return}
  const ok=e.target.dataset.ok, no=e.target.dataset.no;
  if(ok&&confirm('¿Enviar este mensaje?')){await post('ui/confirmar',{id:+ok});location.reload()}
  if(no&&confirm('¿Cancelar este mensaje?')){await post('api/cancelar',{id:+no});location.reload()}
});
let tq;
$('#q').addEventListener('input',()=>{const q=$('#q').value.toLowerCase().trim();
  document.querySelectorAll('#lista .chat').forEach(c=>c.style.display=!q||c.dataset.name.toLowerCase().includes(q)?'':'none');
  clearTimeout(tq); tq=setTimeout(async()=>{const ex=$('#extra'); ex.innerHTML=''; if(q.length<2)return;
    const r=await fetch('api/contactos?q='+encodeURIComponent(q)); const l=await r.json().catch(()=>[]);
    const ya=new Set([...document.querySelectorAll('#lista .chat')].map(c=>c.dataset.jid));
    l.filter(x=>!ya.has(x.jid)).forEach(x=>{const d=document.createElement('div');d.className='chat';d.dataset.jid=x.jid;d.dataset.name=x.nombre;d.innerHTML='<span>👤 '+esc(x.nombre)+'</span><span class="n">contacto</span>';ex.appendChild(d)})},350)});
{{if .S.esperando_qr}}setTimeout(()=>location.reload(),4000);{{else}}setInterval(()=>{if(!editando)location.reload()},60000);{{end}}
</script></body></html>`))

func (a *app) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = page.Execute(w, map[string]interface{}{
		"S": a.stats(), "C": a.listChats("", 150), "P": a.listProgramados(false), "T": time.Now().UnixNano(),
	})
}
