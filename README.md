# server_health_cli

## Config setup

Public-safe defaults are stored in `config.json`.

Real/private server lists should be kept in `config.private.json` (already gitignored).

Quick setup:

```bash
cp config.example.json config.private.json
```

Then edit `config.private.json` with your real values.

Load order at runtime (later overrides earlier):

1. `config.json`
2. `config.private.json`
3. Environment variables

Optional environment overrides:

- `HEALTH_CHECK_PORT=442`
- `SSL_CHECK_PORTS=443,442`
- `TESTING_SERVERS=test1.example.com,test2.example.com`
- `PROD_SERVERS=app1.example.com,app2.example.com`
