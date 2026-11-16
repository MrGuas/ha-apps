# WhatsApp puente

Conecta tu cuenta de WhatsApp a Home Assistant como un **dispositivo vinculado**
(igual que WhatsApp Web). La App guarda el **texto** de tus conversaciones en una
base de datos local, te deja **programar mensajes** desde su pantalla y ofrece una
**API local** para que automatizaciones o asistentes puedan leer, buscar y
proponer envíos, siempre con tu confirmación.

> [!WARNING]
> Usa [whatsmeow](https://github.com/tulir/whatsmeow), una implementación **no
> oficial** del protocolo de WhatsApp. No está afiliada ni respaldada por
> WhatsApp ni por Meta. Usarla puede ir contra sus condiciones de servicio y,
> en teoría, provocar que se restrinja tu número. Úsala con moderación, para
> uso personal y bajo tu responsabilidad.

## Índice

- [Instalación](#instalación)
- [Vincular tu WhatsApp](#vincular-tu-whatsapp)
- [Configuración](#configuración)
- [Programar mensajes](#programar-mensajes)
- [Seguridad de los envíos](#seguridad-de-los-envíos)
- [API](#api)
- [Ejemplos para Home Assistant](#ejemplos-para-home-assistant)
- [Datos y privacidad](#datos-y-privacidad)
- [Problemas frecuentes](#problemas-frecuentes)

## Instalación

1. En Home Assistant ve a **Ajustes → Apps → Tienda de apps**.
2. Menú **⋮ → Repositorios** y añade `https://github.com/MrGuas/ha-apps`.
3. Busca **WhatsApp puente**, pulsa **Instalar** y después **Iniciar**.
4. Activa **Mostrar en la barra lateral** para tener el panel a mano.

La primera instalación compila la App en tu equipo y puede tardar varios minutos
en una Raspberry Pi.

## Vincular tu WhatsApp

1. Abre la App (**Abrir interfaz web** o el panel *WhatsApp* de la barra lateral).
2. En el móvil: **WhatsApp → Ajustes → Dispositivos vinculados → Vincular un dispositivo**.
3. Escanea el código QR que aparece en pantalla.

En unos segundos el estado pasa a **conectado** y empieza a llegar el historial
reciente que WhatsApp comparte con los dispositivos vinculados. Los nombres de los
remitentes se completan poco a poco a medida que llega la agenda.

Si cierras la sesión desde el móvil, la App lo detecta y vuelve a mostrar un QR.

## Configuración

Todas las opciones están en la pestaña **Configuración** de la App.

| Opción | Por defecto | Descripción |
| --- | --- | --- |
| `api_token` | *(vacío)* | Contraseña para usar la API desde fuera de la interfaz. Vacío = la API solo responde a la pantalla de la App. |
| `allowed_send` | `[]` | Lista blanca de destinos a los que las automatizaciones (`origen: auto`) pueden enviar **sin** confirmación. Nombre exacto del chat, número o JID. |
| `notify_service` | *(vacío)* | Servicio `notify` de la app móvil de Home Assistant para los avisos y los botones **Enviar / Cancelar**. Por ejemplo `notify.mobile_app_mi_movil`. Vacío = solo se confirma desde la pantalla. |
| `country_code` | `34` | Prefijo que se añade a los números escritos sin él. Los que empiezan por `+` o `00` no se tocan. |
| `max_per_day` | `20` | Máximo de mensajes enviados al día. |
| `late_minutes` | `30` | Si un mensaje lleva más retraso que esto (por ejemplo, Home Assistant estaba apagado), no se envía y se avisa. |

Ejemplo:

```yaml
api_token: "una-cadena-larga-y-aleatoria"
allowed_send:
  - "Familia"
  - "+34600111222"
notify_service: notify.mobile_app_mi_movil
country_code: "34"
max_per_day: 20
late_minutes: 30
```

Para encontrar tu servicio de notificación: **Herramientas para desarrolladores →
Acciones** y busca `notify.mobile_app_`.

## Programar mensajes

Desde la pantalla de la App:

1. Busca el chat o contacto y púlsalo.
2. Escribe el mensaje (admite `*negrita*`, `_cursiva_` y `~tachado~`; verás una vista previa).
3. Elige fecha y hora y pulsa **Programar**, o **Enviar ahora**.

La lista **Programados** muestra el estado de cada mensaje (`programado`,
`por confirmar`, `enviado`, `cancelado`, `caducado`, `fallido`) y permite
confirmarlos o cancelarlos.

Antes de enviar, la App muestra *escribiendo…* unos segundos, como haría una persona.

## Seguridad de los envíos

Lo que se programa depende de **quién** lo pide:

| Origen | Quién | Comportamiento |
| --- | --- | --- |
| `pantalla` | Tú, desde la interfaz de Home Assistant | Se envía tal cual a su hora. |
| `api` | Un asistente o integración por la API | **Siempre** queda *por confirmar* hasta que pulses **Enviar** en la notificación o en la pantalla. |
| `auto` | Automatizaciones | Se envía sin preguntar **solo** si el destino está en `allowed_send`; si no, pide confirmación. |

Además, en todos los casos:

- **Límite diario** (`max_per_day`).
- **Margen de retraso** (`late_minutes`): un mensaje que llega tarde no se envía.
  Uno *por confirmar* que nadie confirma a tiempo caduca.
- **Ya has escrito en ese chat**: si escribiste en el chat después de programar el
  mensaje, la App te pregunta antes de enviarlo.
- **Destino exacto**: la API solo acepta un JID, un número o el nombre **exacto**
  de un chat. Si el nombre es ambiguo devuelve los candidatos y no envía nada.
- La pantalla y las confirmaciones solo funcionan a través de la interfaz de Home
  Assistant (Ingress), así que exigen tu sesión.

## API

La API escucha en el puerto `8080` **dentro de la red interna de Home Assistant**;
no se publica en tu red local. Desde Home Assistant (por ejemplo con
`rest_command`) se llama por el nombre de host de la App, que aparece en su
pestaña **Información** y suele ser:

```text
http://b28c179e-whatsapp-puente:8080
```

Todas las llamadas necesitan la cabecera:

```text
Authorization: Bearer <api_token>
```

Las respuestas son JSON. Las fechas se devuelven en la zona horaria de Home Assistant.

### Leer

| Método | Ruta | Parámetros | Devuelve |
| --- | --- | --- | --- |
| `GET` | `/api/status` | — | Estado de la conexión, número de mensajes y chats, programados pendientes. |
| `GET` | `/api/chats` | `q` (filtro), `limit` (50) | Chats ordenados por actividad reciente. |
| `GET` | `/api/messages` | `chat` (JID o parte del nombre), `since` (`24h`), `limit` (500) | Mensajes de un chat en orden cronológico. |
| `GET` | `/api/search` | `q` (texto), `since` (`365d`), `limit` (100) | Mensajes que contienen el texto, en todos los chats. |
| `GET` | `/api/contactos` | `q` (mín. 2 letras) | Contactos de la agenda que coinciden. |
| `GET` | `/api/programados` | `activos=1` (opcional) | Mensajes programados (activos y de los últimos 7 días). |

`since` acepta `12h`, `7d`, `2026-11-01` o `2026-11-01 08:00`.

### Programar y cancelar

`POST /api/programar`

```json
{
  "to": "Familia",
  "text": "La lavadora ha terminado 🧺",
  "when": "2026-11-20T08:30",
  "origen": "auto"
}
```

| Campo | Obligatorio | Descripción |
| --- | --- | --- |
| `to` | sí | JID, número de teléfono o nombre **exacto** del chat. |
| `text` | sí | Texto del mensaje. |
| `when` | no | `2026-11-20T08:30`, `2026-11-20 08:30`, RFC 3339 o `ahora` (por defecto). |
| `origen` | no | `auto` para automatizaciones; cualquier otro valor se trata como `api`. |

Respuesta:

```json
{ "ok": true, "id": 42, "estado": "pendiente", "a": "Familia", "cuando": "20/11/2026 08:30" }
```

Si `to` no identifica un único chat, responde `409` con `candidatos`.

`POST /api/cancelar` con `{"id": 42}` cancela un mensaje pendiente o por confirmar.

`POST /api/send` es un alias de `/api/programar` que se mantiene por compatibilidad.

### Ejemplo con curl

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://b28c179e-whatsapp-puente:8080/api/messages?chat=Familia&since=2d"
```

## Ejemplos para Home Assistant

### `rest_command` para enviar desde automatizaciones

```yaml
# configuration.yaml
rest_command:
  whatsapp_enviar:
    url: http://b28c179e-whatsapp-puente:8080/api/programar
    method: POST
    headers:
      Authorization: !secret whatsapp_puente_token
    content_type: application/json
    payload: >-
      {"to": {{ to | tojson }}, "text": {{ text | tojson }}, "origen": "auto"}
```

```yaml
# secrets.yaml
whatsapp_puente_token: "Bearer una-cadena-larga-y-aleatoria"
```

```yaml
# Automatización
triggers:
  - trigger: state
    entity_id: sensor.lavadora_estado
    to: "terminado"
actions:
  - action: rest_command.whatsapp_enviar
    data:
      to: "Familia"
      text: "La lavadora ha terminado 🧺"
```

Recuerda añadir `Familia` a `allowed_send`; si no, cada envío pedirá confirmación.

### Sensor con el estado de la conexión

```yaml
rest:
  - resource: http://b28c179e-whatsapp-puente:8080/api/status
    headers:
      Authorization: !secret whatsapp_puente_token
    scan_interval: 300
    binary_sensor:
      - name: WhatsApp conectado
        value_template: "{{ value_json.conectado }}"
        device_class: connectivity
    sensor:
      - name: WhatsApp mensajes programados
        value_template: "{{ value_json.programados }}"
```

## Datos y privacidad

- Todo se queda en tu Home Assistant, en `/data` de la App:
  - `sesion.db`: claves de la sesión vinculada.
  - `mensajes.db`: texto de los mensajes, chats, nombres y programados.
- **Solo se guarda texto.** De fotos, vídeos, audios, documentos y stickers solo
  se apunta que existieron (y su pie de foto); no se descarga ningún archivo.
- Los estados de WhatsApp no se guardan.
- Las bases de datos **no se incluyen en las copias de seguridad** de Home
  Assistant. Tras restaurar una copia tendrás que volver a vincular el móvil, y el
  historial se recupera de nuevo desde WhatsApp en la medida en que este lo comparta.
- Para borrarlo todo, desinstala la App o cierra la sesión del dispositivo
  vinculado desde el móvil.

## Problemas frecuentes

**El QR no aparece o caduca enseguida.** Los QR duran unos segundos y la página
se recarga sola. Si se queda en *error al conectar*, revisa el registro de la App.

**Los remitentes aparecen como números.** WhatsApp envía la agenda y los nombres
poco a poco. La App los repasa sola; también puedes forzarlo con
`GET /api/fix-names`.

**No me llegan las notificaciones de confirmación.** Comprueba que
`notify_service` existe (prueba a llamarlo desde *Herramientas para
desarrolladores*) y que la app móvil de Home Assistant tiene permiso para
notificaciones.

**`401 no autorizado`.** Falta la cabecera `Authorization: Bearer …` o el token no
coincide. Con `api_token` vacío la API solo responde a la interfaz.

**`409 destino ambiguo o desconocido`.** Usa el nombre exacto del chat, el número
con prefijo o el JID que devuelve `/api/chats`.

¿Algo más? Abre una incidencia en
[GitHub](https://github.com/MrGuas/ha-apps/issues).
