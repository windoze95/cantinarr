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
| Anthropic | `claude-opus-5-5`, `claude-fable-5-1`, `claude-sonnet-5-5`, `claude-haiku-5-5` | Anthropic Messages API (`/v1/messages`, streaming) | Current API IDs from Anthropic's model overview. All support tool use and adaptive thinking; Opus 5.5, Fable 5.1, and Sonnet 5.5 reject thinking disabled; their validation probes use low effort with a bounded reasoning budget. |
| OpenAI | `gpt-4.1-mini`, `gpt-5.4-mini`, `gpt-5.5`, `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-5.6-luna`, `gpt-6-astra`, `gpt-6-sol`, `gpt-6-luna`, `gpt-6.1-sol` | OpenAI Chat Completions; GPT-6 Astra and GPT-6.1 Sol use Responses (both streaming) | These are public API IDs confirmed by OpenAI's API model pages, independently of matching Codex OAuth selectors. GPT-4.1 mini remains first/default as the supported lower-cost option. On the official `api.openai.com` endpoint, GPT-6 Sol and Luna require `reasoning_effort: none` for Chat Completions function calling; Cantinarr sends `none` even when profile settings use automatic reasoning. GPT-6 Astra and GPT-6.1 Sol use Responses for validation, chat, and autonomous turns because their Chat Completions endpoints do not support tool calling. A configured OpenAI-compatible base URL keeps its own model and parameter behavior. |
| Google Gemini | `gemini-3.8-flash`, `gemini-3.7-flash`, `gemini-3.6-flash`, `gemini-3.5-flash`, `gemini-3.5-flash-lite`, `gemini-3.1-flash-lite`, `gemini-3.1-pro-preview` | Gemini `streamGenerateContent` / `GenerateContent` | The list uses exact model endpoint names; current Flash models support function calling. Validation uses each model's documented Low or Minimal thinking level with a bounded output budget. Gemini 3.1 Pro is explicitly marked preview. Gemini 2.5 is omitted from recommendations because Google now limits its API to accounts that used it previously, while documenting that existing access continues. |
| xAI Grok | `grok-4.7`, `grok-4.6`, `grok-4.5` | Public xAI Chat Completions (`/v1/chat/completions`, streaming) | Exact public API model IDs; function calling is supported. Cantinarr sends no `reasoning_effort` parameter to Grok. xAI marks Chat Completions as legacy, but documents it as an available API family; this adapter remains on that compatible endpoint. |

## OAuth providers

| Settings provider | OAuth selector values | Request family used by Cantinarr | Notes |
| --- | --- | --- | --- |
| OpenAI OAuth / Codex | `default`, `gpt-6-astra`, `gpt-6.1-sol`, `gpt-6-sol`, `gpt-6-luna`, `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-5.6-luna` | Codex app-server | These are Codex model selectors, sent unchanged in `thread/start`; the app-server uses the linked ChatGPT OAuth account and Responses path. OpenAI documents the GPT-6 selectors for Codex separately from API endpoint IDs. Its `model/list` response is a client catalog, not an entitlement check, so an accepted test turn is the only account-level proof. If the account lacks rollout or plan access, settings validation returns a sanitized unavailable-model message and saves nothing. |
| xAI Grok OAuth | `grok-4.6`, `grok-4.5` | Grok Build OAuth proxy at `https://cli-chat-proxy.grok.com/v1/responses` | This list comes from xAI Grok Build's own OAuth model catalog. OAuth requests use the proxy's Responses API and Grok request-context headers, including `x-grok-client-version: 1.0.13`. Cantinarr retains its own client identifier. The version is the proxy's compatible protocol floor observed on 2026-10-09; omitting it returns HTTP 426 after OAuth succeeds. These requests must not be sent to `api.x.ai` as public API-key Chat Completions. |

## Configuration and validation behavior

- The local OpenAI-compatible provider intentionally has no model catalog. Its
  custom model ID, administrator-supplied base URL, and optional key remain
  supported.
- Unknown or previously saved model strings remain stored as-is. Updating the
  recommendations does not rewrite user or shared settings. A selected
  unknown, retired, or inaccessible model is reported as unavailable when the
  upstream returns a model-specific error; unrelated invalid requests remain
  generic validation errors.
- GPT-6 Astra and GPT-6.1 Sol use the official OpenAI Responses endpoint with the exact
  selected API model ID. Requests use `store: false` and replay encrypted
  reasoning, assistant phase, function calls, and tool results between turns.
  Their save probes use low reasoning and a sufficient output budget; inherited
  None/Minimal pins also use Low for these models without rewriting settings.
  The separate Codex OAuth selection uses the Codex app-server. Its probes
  use low effort for documented selectors and a 16,000-token observed output
  ceiling, since the former 256-token cutoff could interrupt reasoning before
  any visible answer. The existing timeout also bounds the check; app-server
  usage notifications provide an observed ceiling rather than a hard request
  token limit.
- GPT-6 endpoint-specific request constraints apply only when the OpenAI
  provider targets `api.openai.com`. A custom OpenAI-compatible URL retains
  its own model behavior, including configured reasoning effort.
- No credentials are created or changed by catalog updates. Settings probes
  use the configured credential against a mock in tests; a successful mock
  proves request serialization only, not entitlement or live account access.
- Codex completion errors can carry the actionable detail in `message` while
  `codexErrorInfo` says only `other`. Cantinarr classifies that message before
  discarding upstream details from the user response. HTTP 426 receives an
  explicit client-upgrade message instead of an invalid-credential diagnosis.
- Grok OAuth uses a bounded 16,000-token validation allowance, since its
  hidden reasoning can consume a smaller budget before visible text arrives.
  Following the official Grok Build client, stateless Responses request
  encrypted reasoning and replay it with function calls and results. No
  OpenAI-specific effort parameter is sent to xAI.
- Container builds and the CI protocol smoke test pin Codex app-server 0.162.0
  with verified archive checksums for both Linux architectures. Its official
  bundled catalog includes GPT-6 Astra, Sol, Luna, and GPT-6.1 Sol; the previous
  July 0.144.3 catalog had no GPT-6 metadata. Updating the runtime
  supplies current model capabilities while retaining the isolated app-server
  boundary, existing OAuth credentials, and exact selected model.
- Grok OAuth ignores SSE comment/empty keepalive events before JSON decoding.
  Nonempty malformed JSON and missing terminal responses still fail the turn.

## Live validation evidence

On 2026-10-09, the normal settings Save test on the PR preview built from
`7c55786913734adb435db7168544cd7d8d4a57a8` passed for all 24 public API choices
above: OpenAI 10/10, Anthropic 4/4, Gemini 7/7, and xAI 3/3. These were bounded
tests using existing configured credentials and prove access for that account
at that time. They do not establish access for other accounts.

The same preview passed Codex `default`, `gpt-5.6-sol`, and `gpt-5.6-luna`.
The four GPT-6 Codex selectors failed with an upstream detail that the old
completion decoder discarded; `gpt-5.6-terra` reached the existing 60-second
timeout. Those results do not establish an account-entitlement cause. A
separate user-initiated xAI OAuth test reached the correct Responses proxy but
failed its HTTP 426 client-version gate, motivating the header correction.
The local compatible provider was not configured in that environment.
Failed saves preserved the prior configuration, and the original shared
provider/model and container image were restored through the Unraid GUI.

The updated preview at head `a9cc659dff3bd2e3d2305b37ccc3a8c0340a578d`,
merge checkout `8f4bae085a058b526002a44469117d9424a22c8e`, passed all eight
Codex choices between 17:45 and 17:52 UTC, including all four GPT-6 choices
and the previously timed-out GPT-5.6 Terra. Grok OAuth passed the client-version
gate but failed stream decoding with `unexpected end of JSON input`. Empty
SSE events reproduce that error in the SDK; the parser correction has mock
coverage and awaits live revalidation.

## Official sources

All sources below were checked on 2026-10-09.

- Anthropic: [model overview and exact API IDs](https://platform.claude.com/docs/en/models/overview), [Messages API](https://platform.claude.com/docs/en/api/messages/create), [per-model thinking configuration](https://platform.claude.com/docs/en/build-with-claude/thinking).
- OpenAI API: [GPT-6 Astra](https://developers.openai.com/api/docs/models/gpt-6-astra), [GPT-6 Sol](https://developers.openai.com/api/docs/models/gpt-6-sol), [GPT-6 Luna](https://developers.openai.com/api/docs/models/gpt-6-luna), [GPT-6.1 Sol](https://developers.openai.com/api/docs/models/gpt-6.1-sol), [GPT-5.6 Sol](https://developers.openai.com/api/docs/models/gpt-5.6-sol), [GPT-5.6 Terra](https://developers.openai.com/api/docs/models/gpt-5.6-terra), [GPT-5.6 Luna](https://developers.openai.com/api/docs/models/gpt-5.6-luna), [GPT-5.5](https://developers.openai.com/api/docs/models/gpt-5.5), [GPT-5.4 mini](https://developers.openai.com/api/docs/models/gpt-5.4-mini), and [GPT-4.1 mini](https://developers.openai.com/api/docs/models/gpt-4.1-mini). The separate [Codex model catalog](https://developers.openai.com/codex/models) lists the Codex model selections and account/rollout caveat. The [Codex app-server OAuth integration](https://developers.openai.com/siwc/token-sharing-open-source/codex-app-server) documents passing a selected model in `thread/start` and notes that the bundled `model/list` catalog is not an entitlement check.
- Codex protocol: [app-server turn effort and token usage](https://developers.openai.com/codex/app-server).
- Codex runtime: [0.162.0 release and archive checksums](https://github.com/openai/codex/releases/tag/rust-v0.162.0), [0.162.0 bundled model catalog](https://github.com/openai/codex/blob/rust-v0.162.0/codex-rs/models-manager/models.json), [previous 0.144.3 catalog](https://github.com/openai/codex/blob/rust-v0.144.3/codex-rs/models-manager/models.json).
- OpenAI Responses: [reasoning and stateless continuation](https://developers.openai.com/api/docs/guides/reasoning), [function calling](https://developers.openai.com/api/docs/guides/function-calling), [streaming](https://developers.openai.com/api/docs/guides/streaming-responses).
- Google: [Gemini API model catalog](https://ai.google.dev/gemini-api/docs/models), [streaming text generation](https://ai.google.dev/gemini-api/docs/generate-content/text-generation), [function calling](https://ai.google.dev/gemini-api/docs/function-calling), [GenerateContent thinking controls](https://ai.google.dev/gemini-api/docs/generate-content/thinking).
- xAI public API: [Grok 4.7](https://docs.x.ai/developers/models/grok-4.7), [Grok 4.6](https://docs.x.ai/developers/models/grok-4.6), [Grok 4.5](https://docs.x.ai/developers/models/grok-4.5), [legacy Chat Completions](https://docs.x.ai/developers/model-capabilities/legacy/chat-completions).
- xAI OAuth / Grok Build: [official model settings](https://docs.x.ai/build/settings), [enterprise endpoint/auth guidance](https://docs.x.ai/build/enterprise), [Grok Build OAuth model catalog](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-models/default_models.json), [Responses request implementation](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-sampler/src/client.rs), [official proxy URL constant](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-env/src/lib.rs).
- Stream framing: [WHATWG SSE interpretation and keepalive comments](https://html.spec.whatwg.org/multipage/server-sent-events.html#event-stream-interpretation).
