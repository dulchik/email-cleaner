# email-cleaner

file structure:
```
email-cleaner/
├── cmd/
│   ├── desktop/          # CLI or local server entrypoint
│   └── web/              # (later) web server entrypoint
├── internal/
│   ├── auth/             # OAuth & token management
│   ├── gmail/            # Gmail API wrapper
│   ├── parser/           # email parsing & extraction
│   ├── storage/          # DB models & access
│   ├── actions/          # unsubscribe/archive/delete logic
│   └── api/              # REST handlers (for UI)
├── web/                  # optional React frontend for local web UI
├── scripts/              # utilities, DB init, cron, etc.
├── go.mod
└── README.md

```
# How it works

