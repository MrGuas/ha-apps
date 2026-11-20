# Changelog

Todos los cambios relevantes de esta App. El formato sigue
[Keep a Changelog](https://keepachangelog.com/es-ES/1.1.0/) y las versiones,
[Semantic Versioning](https://semver.org/lang/es/).

## [0.4.1] - 2026-11-20

### Cambiado
- Los envíos automáticos a destinos de la lista blanca (origen `auto`) ya no piden confirmación
  cuando has escrito en ese chat después de programarlos. Así funcionan las series de
  recordatorios en grupos activos.

## [0.4.0] - 2026-11-16

### Añadido
- Opción `country_code` para el prefijo de país de los números sin prefijo (antes fijo en 34).
- Se aceptan números con `00`, espacios, guiones y paréntesis.
- Traducciones de las opciones (español e inglés), icono y logo.
- Documentación completa: instalación, API, ejemplos y problemas frecuentes.

### Cambiado
- `notify_service` ya no tiene un valor por defecto; si está vacío, las confirmaciones
  se hacen solo desde la pantalla de la App.
- Los mensajes programados por la API se registran con origen `api` (los antiguos `claude`
  se siguen mostrando igual).
- Dependencias de Go fijadas (`go.sum`) para compilaciones reproducibles.

## [0.3.0]

### Añadido
- Programador de mensajes con tres orígenes: pantalla (sin restricciones), API (siempre con
  confirmación) y automáticos (lista blanca).
- Confirmación desde notificaciones del móvil con botones Enviar / Cancelar.

## [0.2.1]

### Corregido
- No bloquear la base de datos al poner nombres en las menciones.

## [0.2.0]

### Añadido
- Nombres de remitentes (LID → teléfono → contacto), menciones con nombre y repaso de nombres.

## [0.1.0]

- Primera versión: archivo de texto de las conversaciones y API local.
