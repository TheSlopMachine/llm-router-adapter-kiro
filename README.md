# Kiro AI Adapter for llm-router

Kiro AI adapter for `llm-router`, using Kiro's AWS CodeWhisperer-compatible streaming API.

## Features

- OAuth-style credentials with automatic refresh
- Dashboard device-login flow for AWS Builder ID and AWS IAM Identity Center
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
  "profile_arn": "arn:aws:codewhisperer:...",
  "auth_method": "imported",
  "region": "us-east-1"
}
```

The interactive auth flow stores these automatically after device login. If a refresh token is present, the adapter rotates access tokens automatically.

## License

MIT
