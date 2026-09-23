# Networking and domains

Shoulders maps vind container ports `80` and `443` to your host for Dex, Grafana and Headlamp.

- `platform.domain` overrides the default `*.localhost` gateway hosts.
- Dex default issuer: `https://dex.127.0.0.1.sslip.io`, or `https://dex.<platform.domain>`.
- Grafana default: `http://grafana.localhost`; Headlamp: `http://headlamp.localhost`;
  reporter: `http://reporter.localhost`.

If you change cluster networking settings, recreate the cluster:

```bash
shoulders down
shoulders up
```

vind freezes cluster DNS at creation; `shoulders start` reconciles CoreDNS forwarding
against the machine's current nameservers on every boot.
