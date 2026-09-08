# Model providers

The review pipeline depends on `Provider.Complete`, not a vendor SDK. The shared Chat Completions transport handles bounded responses, cancellation, retries and metadata; model-specific capability settings control the wire protocol.

| Provider | Credential | Default endpoint | Selection |
| --- | --- | --- | --- |
| `none` | None | None | Deterministic review only |
| `deepseek` | `DEEPSEEK_API_KEY` | `https://api.deepseek.com` | Existing direct provider |
| `orcarouter` | `ORCAROUTER_API_KEY` | `https://api.orcarouter.ai/v1` | Optional gateway preset |
| `openai-compatible` | `AEGIS_API_KEY` | Explicit HTTPS URL required | Explicit model and endpoint opt-in |

Configuration is loaded **only** with `--config`. API keys belong in environment variables or an ignored `.env`, never in JSON. CI should always use `--agent-no-dotenv` and trusted workflow inputs.

## OrcaRouter

```sh
aegis review --repo . --base master --head HEAD \
  --agent-provider orcarouter --agent-model deepseek/deepseek-v4-flash \
  --agent-no-dotenv --analyzers all --format html --output review.html
```

Set `ORCAROUTER_API_KEY` through your shell secret manager or GitHub Actions secret. The model identifier above is a configurable preset, not a guarantee of ongoing model availability. Check the gateway's model catalog. Start provider comparisons with the same explicitly selected model, prompt, corpus and budgets. Automatic routing changes the experiment and should be evaluated separately.

## Other compatible models

Create a non-secret configuration file outside an untrusted PR checkout:

```json
{
  "version": 1,
  "agent": {
    "provider": "openai-compatible",
    "model": "your-tested-model-id",
    "base_url": "https://your-provider.example/v1",
    "api_key_env": "AEGIS_API_KEY",
    "thinking": false,
    "request_timeout": "60s",
    "max_retries": 2,
    "capabilities": {
      "tool_calling": true,
      "json_output": true,
      "reasoning": "none",
      "replay_reasoning": false,
      "token_limit_field": "max_tokens"
    }
  }
}
```

```sh
aegis review --config /trusted/path/provider.json \
  --agent-allow-custom-endpoint --agent-no-dotenv \
  --base master --head HEAD --output review.html
```

Only enable capabilities supported by the selected model. Unknown models use conservative defaults and report reduced coverage when tools/JSON protocol features are unavailable. `json_output` requests JSON object mode, **not** strict JSON Schema validation. Aegis independently validates the returned candidate structure. `reasoning` accepts `none`, `effort`, or `deepseek`; `replay_reasoning` controls whether reasoning content is returned to the model in a tool conversation. The report does not publish private reasoning traces.

`--agent-timeout` bounds the complete reasoning loop. `--agent-request-timeout` bounds one model completion, including its HTTP retries and backoff. `--agent-max-retries=0` disables retries. Retrying a request may incur provider charges if the provider processed a previous attempt; Aegis does not promise exactly-once billing.

## Trace and privacy

Each report records per-turn provider, requested/reported model, request ID when supplied, fallback information, attempts, HTTP status and latency. An absent field means unknown, not a successful no-fallback guarantee. Gateway model names are provider-reported metadata, not proof of upstream identity. Token usage reflects what responses report; missing usage and lost responses can undercount actual billed usage. Aegis does not invent a dollar cost from a token count.

The gateway's ZDR statement applies to its own infrastructure; upstream retention and routing policies need separate verification. See [OrcaRouter ZDR](https://docs.orcarouter.ai/operations/zero-data-retention) and [response metadata](https://docs.orcarouter.ai/routing/response-headers). Integration does not mean Aegis endorses a provider or has entered a partnership. Any future referral link must clearly disclose its financial relationship; no referral attribution is silently added to requests.
