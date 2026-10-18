# Sites storage and cost check

Written by AI (GPT-6.1 Sol), 2026-10-18. Read-only check; no upload, deployment, storage write, credit purchase or account change.

Sites currently includes Plus/Pro hosting and waives Enterprise hosting charges. Shared usage/storage limits still apply. **The documentation does not confirm this account's allowance for 18.54 GB / 484,000 objects.** Keep it local. [Official Help Center](https://help.openai.com/en/articles/20001339-creating-and-using-chatgpt-sites)

| Question | Verified result | Evidence |
| --- | --- | --- |
| Is Sites hosting included? | Plus/Pro: yes, currently. Enterprise: running/hosting charges currently waived. | [Help Center FAQ](https://help.openai.com/en/articles/20001339-creating-and-using-chatgpt-sites); [official pricing](https://learn.chatgpt.com/docs/pricing#how-much-does-sites-cost) also says Sites is included with eligible plans during public beta. |
| Total object-storage capacity per site? | R2 has “No fixed storage limit”. This is the storage used for Nice Weather files. | [Official Sites limits](https://learn.chatgpt.com/docs/sites#understand-limits-and-unsupported-uses); local `hosting/sites/.openai/hosting.json` binds R2 as `WEATHER`. |
| Database capacity? | D1 is limited to 10 GB per site; Nice Weather declares no D1 binding. This limit does not apply to its weather objects. | Same official limits and local manifest. |
| Shared limits or file/object count limit? | Shared storage/usage limits apply across Sites, but no numeric shared allowance or object-count cap is published in the fetched pages or exposed tools. | [Help Center: Usage and limits](https://help.openai.com/en/articles/20001339-creating-and-using-chatgpt-sites); official Learn pages and 25 native Sites tool schemas. |
| Maximum individual object or upload-request size? | Not established for Sites by those sources. | Same documentation and tool-schema inspection. |
| Storage, request or bandwidth overage rates? | No numeric allowance or per-GB, per-operation or egress rate established. Future Enterprise hosting charges depend on the workspace agreement and may use credits or dollars. | [Help Center usage/hosting FAQ](https://help.openai.com/en/articles/20001339-creating-and-using-chatgpt-sites). |
| This account's remaining capacity, credits or automatic overage behavior? | Unknown. The read-only `sites_get_site` result confirms an active site but exposes no billing, usage, storage or quota fields. No available native Sites tool supplies these figures. | Actual `mcp__codex_apps__sites_get_site` call using the project ID copied from the local hosting manifest. No credentials or account details retained in this report. |

The [Help Center](https://help.openai.com/en/articles/20001339-creating-and-using-chatgpt-sites) separates Codex building/editing usage from hosting, traffic and storage usage. A shared limit can block new Site creation. It gives no automatic-overage policy or account headroom. No upload was attempted to discover a limit.

The [storage guide](https://learn.chatgpt.com/docs/sites/data-and-connections) identifies R2 as file storage. The installed Sites skill's `references/storage.md` explains provisioning and bindings but supplies no storage pricing or quota. The repository hosting README's earlier “No paid hosting service is used” is a project note, not a billing guarantee. Cloudflare retail R2 rates and the OpenAI API's file-search rates are not evidence of Sites charges.

Decision: **currently included hosting is verified for Plus/Pro; an unlimited free storage allowance at any size is not verified.** No paid storage, upload or cost experiment has been authorised.
