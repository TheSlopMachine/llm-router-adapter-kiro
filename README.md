# Kiro AI Adapter for llm-router

Kiro AI adapter for `llm-router`, using Kiro's AWS CodeWhisperer-compatible streaming API.

## Features

- Interactive dashboard device login for AWS Builder ID and AWS IAM Identity Center
- Automatic token refresh for credentials created by device login
- Non-streaming and streaming chat completions
- Hardcoded Kiro model catalog fallback
- AWS EventStream decoding for Kiro responses

## Installation

Add to `adapters.conf`:

```txt
github.com/TheSlopMachine/llm-router-adapter-kiro main
```

Then rebuild `llm-router`.

## Credential Shape

```json
{
  "access_token": "eyJ...",
  "refresh_token": "aorAAAAAG...",
  "expires_at": "2026-05-21T14:30:00Z",
  "auth_method": "builder-id",
  "client_id": "client-id-from-device-login",
  "client_secret": "client-secret-from-device-login",
  "region": "us-east-1"
}
```

The interactive auth flow stores these automatically after device login and reuses them for refresh.

## License

MIT
