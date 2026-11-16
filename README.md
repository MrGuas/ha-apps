<p align="center">
  <img src="whatsapp_puente/logo.png" alt="WhatsApp puente" width="400">
</p>

<h1 align="center">Apps para Home Assistant</h1>

<p align="center">
  <a href="https://my.home-assistant.io/redirect/supervisor_add_addon_repository/?repository_url=https%3A%2F%2Fgithub.com%2FMrGuas%2Fha-apps"><img src="https://my.home-assistant.io/badges/supervisor_add_addon_repository.svg" alt="Añadir repositorio a Home Assistant"></a>
</p>

<p align="center">
  <a href="https://github.com/MrGuas/ha-apps/actions/workflows/ci.yaml"><img src="https://github.com/MrGuas/ha-apps/actions/workflows/ci.yaml/badge.svg" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/MrGuas/ha-apps" alt="Licencia"></a>
  <img src="https://img.shields.io/badge/Home%20Assistant-App-41BDF5?logo=homeassistant&logoColor=white" alt="Home Assistant App">
</p>

Repositorio de Apps (antes llamadas *complementos* o *add-ons*) para Home Assistant OS
y Home Assistant Supervised.

## Apps

| App | Descripción | Arquitecturas |
| --- | --- | --- |
| [**WhatsApp puente**](whatsapp_puente) | Guarda el texto de tus conversaciones de WhatsApp, programa mensajes y ofrece una API local con confirmación en el móvil. | `amd64` · `aarch64` |

## Instalación

Pulsa el botón de arriba o, a mano:

1. **Ajustes → Apps → Tienda de apps**.
2. Menú **⋮ → Repositorios**.
3. Añade `https://github.com/MrGuas/ha-apps`.
4. Busca la App, instálala y abre su pestaña **Documentación**.

## English

Home Assistant apps (add-ons). **WhatsApp puente** links your WhatsApp account as a
companion device, keeps a local, searchable text archive of your chats, lets you
schedule messages from a built-in panel and exposes a local API for automations and
assistants, with send confirmation on your phone. The UI and API are in Spanish;
option descriptions are translated to English.

## Aviso

WhatsApp puente usa [whatsmeow](https://github.com/tulir/whatsmeow), una
implementación no oficial del protocolo de WhatsApp. Este proyecto no está
afiliado, asociado ni respaldado por WhatsApp ni por Meta Platforms. «WhatsApp» es
una marca de sus respectivos titulares. Úsalo bajo tu responsabilidad.

## Contribuir

Las incidencias y *pull requests* son bienvenidas. Consulta [CONTRIBUTING.md](CONTRIBUTING.md).

## Licencia

[MIT](LICENSE). Las dependencias mantienen sus propias licencias (whatsmeow: MPL-2.0).
