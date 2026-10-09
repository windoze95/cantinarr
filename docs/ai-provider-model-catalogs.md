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
| OpenAI | `gpt-4.1-mini`, `gpt-5.4-mini`, `gpt-5.5`, `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-5.6-luna` | OpenAI Chat Completions (streaming) | These public API IDs are confirmed by OpenAI's API model pages, independently of any matching Codex OAuth names. GPT-4.1 mini remains first/default as the supported lower-cost option and supports tool calling, but has no reasoning control. GPT-5.4 mini, GPT-5.5, and the GPT-5.6 models support reasoning effort through `xhigh`. The UI only offers that control for models that support it. |
| Google Gemini | `gemini-3.8-flash`, `gemini-3.7-flash`, `gemini-3.6-flash`, `gemini-3.5-flash`, `gemini-3.5-flash-lite`, `gemini-3.1-flash-lite`, `gemini-3.1-pro-preview` | Gemini `streamGenerateContent` / `GenerateContent` | The list uses exact model endpoint names; current Flash models support function calling. Gemini 3.1 Pro is explicitly marked preview. Gemini 2.5 is omitted from recommendations because Google now limits its API to accounts that used it previously, while documenting that existing access continues. |
| xAI Grok | `grok-4.7`, `grok-4.6`, `grok-4.5` | Public xAI Chat Completions (`/v1/chat/completions`, streaming) | Exact public API model IDs; function calling is supported. Cantinarr sends no `reasoning_effort` parameter to Grok. xAI marks Chat Completions as legacy, but documents it as an available API family; this adapter remains on that compatible endpoint. |

## OAuth providers

| Settings provider | OAuth selector values | Request family used by Cantinarr | Notes |
| --- | --- | --- | --- |
| OpenAI OAuth / Codex | `default`, `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-5.6-luna` | Codex app-server | These values are sent through the Codex OAuth app-server path; `default` delegates model choice to Codex. The previous OAuth list also contained `gpt-6.1-sol`, `gpt-6-astra`, `gpt-6-sol`, and `gpt-6-luna`. Julian reported GPT-6 selections failing in Cantinarr, so those OAuth choices were removed. The strings are not globally invalid: OpenAI separately documents the same four strings as public API model IDs. That does not establish that a linked Codex account can use them or that Cantinarr's Chat Completions adapter supports the API-specific capabilities. No GPT-6 options are currently offered by Cantinarr. |
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
- OpenAI API: [GPT-5.6 Sol](https://developers.openai.com/api/docs/models/gpt-5.6-sol), [GPT-5.6 Terra](https://developers.openai.com/api/docs/models/gpt-5.6-terra), [GPT-5.6 Luna](https://developers.openai.com/api/docs/models/gpt-5.6-luna), [GPT-5.5](https://developers.openai.com/api/docs/models/gpt-5.5), [GPT-5.4 mini](https://developers.openai.com/api/docs/models/gpt-5.4-mini), [GPT-4.1 mini](https://developers.openai.com/api/docs/models/gpt-4.1-mini). Codex OAuth is a different request/authentication path: [Codex models](https://developers.openai.com/codex/models).
- GPT-6 clarification: OpenAI separately documents [`gpt-6-astra`](https://developers.openai.com/api/docs/models/gpt-6-astra), [`gpt-6.1-sol`](https://developers.openai.com/api/docs/models/gpt-6.1-sol), [`gpt-6-sol`](https://developers.openai.com/api/docs/models/gpt-6-sol), and [`gpt-6-luna`](https://developers.openai.com/api/docs/models/gpt-6-luna) as API IDs, and lists the corresponding model choices in [Codex model guidance](https://developers.openai.com/codex/models). This PR does not expose those API IDs or OAuth selectors in Cantinarr because Julian reports GPT-6 choices fail in Cantinarr. API endpoint support is model-specific: GPT-6 Astra lists Chat Completions and function calling; GPT-6.1 Sol specifies Responses for tool calling and Chat Completions without tools; GPT-6 Sol and Luna permit Chat Completions function calling only with `reasoning_effort: none`. The current API adapter sends Chat Completions with tools, generally without a pinned effort. Availability for an OpenAI API project or a Codex OAuth account remains account-dependent and was not live-tested. Matching strings in the two vendor catalogs do not imply the same authentication, endpoint, parameter, or entitlement behavior.
- Google: [Gemini API model catalog](https://ai.google.dev/gemini-api/docs/models), [streaming text generation](https://ai.google.dev/gemini-api/docs/generate-content/text-generation), [function calling](https://ai.google.dev/gemini-api/docs/function-calling).
- xAI public API: [Grok 4.7](https://docs.x.ai/developers/models/grok-4.7), [Grok 4.6](https://docs.x.ai/developers/models/grok-4.6), [Grok 4.5](https://docs.x.ai/developers/models/grok-4.5), [legacy Chat Completions](https://docs.x.ai/developers/model-capabilities/legacy/chat-completions).
- xAI OAuth / Grok Build: [official model settings](https://docs.x.ai/build/settings), [enterprise endpoint/auth guidance](https://docs.x.ai/build/enterprise), [Grok Build OAuth model catalog](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-models/default_models.json), [Responses request implementation](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-sampler/src/client.rs), [official proxy URL constant](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-env/src/lib.rs).
