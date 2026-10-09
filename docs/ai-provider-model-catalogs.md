# AI provider model catalogs

Reviewed against official vendor documentation and source on **2026-10-09**.
These IDs describe provider-specific interfaces, not interchangeable product
names. A model that appears in Codex, ChatGPT, Grok, or another subscription
does not become a public API model ID by sharing a brand name. A vendor may
also restrict a documented API model by account, project, region, or quota;
Cantinarr's settings probe can test the selected account, but this static list
cannot promise access for every credential.

## API-key providers

| Settings provider | Public API model IDs in the catalog | Request family used by Cantinarr | Notes |
| --- | --- | --- | --- |
| Anthropic | `claude-opus-5-5`, `claude-fable-5-1`, `claude-sonnet-5-5`, `claude-haiku-5-5` | Anthropic Messages API (`/v1/messages`, streaming) | Current API IDs from Anthropic's model overview. All support tool use and adaptive thinking; Opus 5.5 and Fable 5.1 always use adaptive thinking. |
| OpenAI | `gpt-5.5`, `gpt-5.4-mini`, `gpt-4.1-mini` | OpenAI Chat Completions (streaming) | These are public API IDs, separate from Codex OAuth model choices. GPT-5.5 and GPT-5.4 mini support the `xhigh` reasoning effort. GPT-4.1 mini is non-reasoning and supports tool calling. |
| Google Gemini | `gemini-3.8-flash`, `gemini-3.7-flash`, `gemini-3.6-flash`, `gemini-3.5-flash`, `gemini-3.5-flash-lite`, `gemini-3.1-flash-lite`, `gemini-3.1-pro-preview` | Gemini `streamGenerateContent` / `GenerateContent` | The list uses exact model endpoint names; current Flash models support function calling. Gemini 3.1 Pro is explicitly marked preview. Gemini 2.5 is omitted from recommendations because Google now limits its API to accounts that used it previously, while documenting that existing access continues. |
| xAI Grok | `grok-4.7`, `grok-4.6`, `grok-4.5` | Public xAI Chat Completions (`/v1/chat/completions`, streaming) | Exact public API model IDs; function calling is supported. Cantinarr sends no `reasoning_effort` parameter to Grok. xAI marks Chat Completions as legacy, but documents it as an available API family; this adapter remains on that compatible endpoint. |

## OAuth providers

| Settings provider | OAuth selector values | Request family used by Cantinarr | Notes |
| --- | --- | --- | --- |
| OpenAI OAuth / Codex | `default`, `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-5.6-luna` | Codex app-server | These are Codex OAuth selectors, not OpenAI API IDs. `default` delegates model choice to Codex. GPT-6 entries were removed after Julian reported they fail in Cantinarr; no public API model IDs are inferred from Codex or ChatGPT surfaces. |
| xAI Grok OAuth | `grok-4.6`, `grok-4.5` | Grok Build OAuth proxy at `https://cli-chat-proxy.grok.com/v1/responses` | This list comes from xAI Grok Build's own OAuth model catalog. OAuth requests use the proxy's Responses API and Grok request-context headers. They must not be sent to `api.x.ai` as public API-key Chat Completions. |

## Configuration and validation behavior

- The local OpenAI-compatible provider intentionally has no model catalog. Its
  custom model ID, administrator-supplied base URL, and optional key remain
  supported.
- Unknown or previously saved model strings remain stored as-is. Updating the
  recommendations does not rewrite user or shared settings. A selected
  unknown, retired, or inaccessible model is reported as unavailable when the
  upstream returns a model-specific error; unrelated invalid requests remain
  generic validation errors.
- No credentials are created or changed by catalog updates. Settings probes
  use the configured credential against a mock in tests; a successful mock
  proves request serialization only, not entitlement or live account access.

## Official sources

All sources below were checked on 2026-10-09.

- Anthropic: [model overview and exact API IDs](https://platform.claude.com/docs/en/models/overview), [Messages API](https://platform.claude.com/docs/en/api/messages/create).
- OpenAI API: [GPT-5.5](https://developers.openai.com/api/docs/models/gpt-5.5), [GPT-5.4 mini](https://developers.openai.com/api/docs/models/gpt-5.4-mini), [GPT-4.1 mini](https://developers.openai.com/api/docs/models/gpt-4.1-mini). Codex OAuth is a different product path: [Codex models](https://developers.openai.com/codex/models).
- Google: [Gemini API model catalog](https://ai.google.dev/gemini-api/docs/models), [streaming text generation](https://ai.google.dev/gemini-api/docs/generate-content/text-generation), [function calling](https://ai.google.dev/gemini-api/docs/function-calling).
- xAI public API: [Grok 4.7](https://docs.x.ai/developers/models/grok-4.7), [Grok 4.6](https://docs.x.ai/developers/models/grok-4.6), [Grok 4.5](https://docs.x.ai/developers/models/grok-4.5), [legacy Chat Completions](https://docs.x.ai/developers/model-capabilities/legacy/chat-completions).
- xAI OAuth / Grok Build: [official model settings](https://docs.x.ai/build/settings), [enterprise endpoint/auth guidance](https://docs.x.ai/build/enterprise), [Grok Build OAuth model catalog](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-models/default_models.json), [Responses request implementation](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-sampler/src/client.rs), [official proxy URL constant](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-env/src/lib.rs).
