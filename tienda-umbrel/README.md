# Tienda Umbrel - Albercoin

Tienda de aplicaciones personalizada para [umbrelOS](https://umbrel.com).

## Cómo añadir esta tienda a tu Umbrel

1. Ve a **Settings → App Store** en tu panel de Umbrel
2. En **Custom App Repositories**, añade:
   ```
   https://github.com/AlbercoinDev/Tienda-Umbrel-Albercoin
   ```
3. La tienda aparecerá automáticamente en tu App Store

## Apps disponibles

### Umbrel Tunnel
Expón cualquier app de tu Umbrel en internet (clearnet) a través de un túnel WireGuard hacia tu propio VPS.
- Túneles ilimitados
- HTTPS automático con Let's Encrypt
- No necesitas abrir puertos en tu router
- Host local automático: `umbrel.local`
- Descarga o copia tu configuración WireGuard

**Requiere:** Un VPS con Ubuntu/Debian donde instalar el servidor Umbrel Tunnel.

```bash
curl -sL https://github.com/AlbercoinDev/umbreltunnel/raw/main/install.sh | bash
```

[Ver repositorio VPS →](https://github.com/AlbercoinDev/umbreltunnel)
