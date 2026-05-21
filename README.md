# Kiro AI Adapter for llm-router

Kiro AI adapter for `llm-router`, using Kiro's AWS CodeWhisperer-compatible streaming API.

## Features

- OAuth-style credentials with automatic refresh
- Dashboard auth flow for importing a Kiro refresh token
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

Only `refresh_token` or `access_token` is required. If a refresh token is present, the adapter rotates access tokens automatically.

## Notes

- The built-in dashboard flow focuses on token import, which fits `llm-router`'s current auth wizard cleanly.
- The implementation is based on the Kiro integration patterns in OmniRoute, adapted to the `llm-router-sdk`.

## License

MIT
