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

The OrcaRouter adapter is implemented in [provider.go](../internal/agent/provider.go), with mocked protocol tests in [openai_compatible_test.go](../internal/agent/openai_compatible_test.go). These tests do not establish live API compatibility or model quality. Live OrcaRouter acceptance remains pending; see [validation](validation.md#尚未完成的外部验收).

### Get a key and select the provider

1. [Register through the Aegis referral link](https://www.orcarouter.ai/ref/ref_7d9895701ff01fc94d85), or visit the [ordinary OrcaRouter website](https://www.orcarouter.ai/). Create a key in your own workspace. The referral link is not an API endpoint or an API key.
2. Set `ORCAROUTER_API_KEY` using your secret manager; do not commit the key or put it in JSON. For an activated GitHub integration, store it in Actions Secrets and pass it explicitly as `provider-api-key`.
3. Build Aegis as described in the [local development instructions](../README.md#local-development), then select the provider explicitly. Run host-mode commands only against a trusted, clean checkout matching the requested Head. `--analyzers all` also requires installed `staticcheck` and `gosec`.

```sh
/tmp/aegis review --repo . --base master --head HEAD \
  --agent-provider orcarouter --agent-model deepseek/deepseek-v4-flash \
  --agent-no-dotenv --analyzers all --format html --output /tmp/aegis-orcarouter-review.html
```

The model identifier above is a configurable preset, not a guarantee of ongoing availability or a record of live validation. Check the gateway's model catalog and the selected model's tool-calling, JSON and reasoning capabilities. Start provider comparisons with the same explicitly selected model, prompt, corpus and budgets. Automatic routing changes the experiment and should be evaluated separately. API calls may incur charges on your workspace.

**GitHub activation boundary:** this revision selects the V1 workflow configuration locally, and its self-review default remains DeepSeek. Merely adding `ORCAROUTER_API_KEY` will not switch it. Online PR, Docker sandbox and external-consumer acceptance remain pending until this revision is pushed and reviewed; local configuration activation is not production certification or a published release. After an audited workflow revision passes the [V1 acceptance checklist](v1-acceptance.md), a reusable caller can explicitly select `provider: orcarouter`, an explicitly checked model ID, and `provider-api-key: ${{ secrets.ORCAROUTER_API_KEY }}`. Live OrcaRouter API acceptance is a separate pending requirement; see the [trusted integration boundary](github-action.md).

Aegis uses the supported Chat Completions endpoint and its own credential variable, `ORCAROUTER_API_KEY`. The partner guide's TOML/Responses example, `ORCA_KEY` name and automatic router are not settings to paste into Aegis unchanged. Interactive PKCE sign-in and the Connect web widget are not implemented by Aegis and are not needed for the current user-supplied-key CLI/Actions design. No authorization widget or referral tracking is injected into review reports.

### Referral disclosure

The maintainer has received an OrcaRouter referral link: <https://www.orcarouter.ai/ref/ref_7d9895701ff01fc94d85>. Under the offered program, the maintainer may receive **5% of eligible paid usage** from workspaces attributed through that link, subject to the program's attribution and settlement terms. This is not a promise that every click, existing account or API request earns commission; do not assume a discount or free credits.

Using the link and provider is optional. DeepSeek direct, other explicitly configured compatible providers and deterministic mode remain available. The README uses the [OrcaRouter logo published on its official website](https://www.orcarouter.ai/orca-logo-classic.png) to identify an optional provider integration; it does not mean every review uses OrcaRouter, that the project has been approved for the public directory, or that OrcaRouter endorses Aegis. The [Built with OrcaRouter directory](https://www.orcarouter.ai/zh-CN/built-with) is independently reviewed and published by OrcaRouter.

The link is an explicit registration option; Aegis does not silently append referral identifiers to inference requests or replace the user's provider choice.

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

The gateway's ZDR statement applies to its own infrastructure; upstream retention and routing policies need separate verification. See [OrcaRouter ZDR](https://docs.orcarouter.ai/operations/zero-data-retention) and [response metadata](https://docs.orcarouter.ai/routing/response-headers). The referral relationship is disclosed [above](#referral-disclosure); it is not a security certification or an endorsement of every upstream provider or model.
