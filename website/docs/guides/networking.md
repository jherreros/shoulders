# Networking and domains

Shoulders maps vind container ports `80` and `443` to your host for Dex, Grafana and Headlamp.

In a topology, each vind cluster declares its own `ports:` (for example a spoke
on `8080`/`8443`) because only one cluster can bind a given host port.

- `platform.domain` overrides the default `*.localhost` gateway hosts.
- Dex default issuer: `https://dex.127.0.0.1.sslip.io`, or `https://dex.<platform.domain>`.
- Grafana default: `http://grafana.localhost`; Headlamp: `http://headlamp.localhost`;
  reporter: `http://reporter.localhost`.
- In a topology, only the hub declares the domain (`platform.domain`, or `domain`
  on the hub entry); a spoke declares none and serves workload hostnames qualified
  with its own name (`<app>.<cluster>.<domain>`). Two clusters declaring the same
  domain are rejected.

If you change cluster networking settings, recreate the cluster:

```bash
shoulders down
shoulders up
```

vind freezes cluster DNS at creation; `shoulders start` reconciles CoreDNS forwarding
against the machine's current nameservers on every boot.
