# Contribuir

¡Gracias por tu interés!

## Incidencias

Antes de abrir una, revisa la sección *Problemas frecuentes* de la documentación de la
App. Incluye la versión de la App, la de Home Assistant, la arquitectura y el registro
de la App (**quitando números de teléfono, nombres y textos de mensajes**).

## Desarrollo

Requisitos: Go 1.26 y un compilador de C (por SQLite).

```bash
cd whatsapp_puente/app
go vet ./...
go build .
```

Para probar la App en Home Assistant, copia la carpeta `whatsapp_puente` a
`/addons/` de tu instalación y aparecerá en **Apps locales**.

## Pull requests

- Una PR por cambio, con una descripción de qué y por qué.
- `gofmt` y `go vet` sin avisos (lo comprueba el CI).
- Si cambias el comportamiento, sube `version` en `config.yaml` y añade una entrada
  en `CHANGELOG.md`.
- Nunca incluyas datos reales: números, tokens, bases de datos ni registros.
