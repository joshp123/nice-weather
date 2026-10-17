# Sites storage and cost check

Written by AI (GPT-6.1 Sol), 2026-10-18. Read-only check; no upload, deployment, storage write, credit purchase or account change.

Sites is included with eligible ChatGPT plans during public beta. Its object storage has no fixed per-site capacity limit. **Neither statement confirms that an arbitrary 18.54 GB / 484,000-object release has a zero-cost allowance on this account.** Keep the worldwide release local, as Josh instructed.

| Question | Verified result | Evidence |
| --- | --- | --- |
| Is Sites included? | Yes, during public beta, for eligible plans; availability depends on plan, region and workspace settings. | [Official pricing: How much does Sites cost?](https://learn.chatgpt.com/docs/pricing#how-much-does-sites-cost) |
| Total object-storage capacity per site? | R2 has “No fixed storage limit”. This is the storage used for Nice Weather files. | [Official Sites limits](https://learn.chatgpt.com/docs/sites#understand-limits-and-unsupported-uses); local `hosting/sites/.openai/hosting.json` binds R2 as `WEATHER`. |
| Database capacity? | D1 is limited to 10 GB per site; Nice Weather declares no D1 binding. This limit does not apply to its weather objects. | Same official limits and local manifest. |
| File/object count limit? | No numeric limit established by the fetched Sites documentation or available tool schemas. | Official Sites overview, data/connections and management pages; 25 exposed native Sites tool schemas. |
| Maximum individual object or upload-request size? | Not established for Sites by those sources. | Same documentation and tool-schema inspection. |
| Free storage, request or bandwidth allowance; overage rates? | No numeric allowance, per-GB price, per-operation price or egress rate established by the fetched Sites pages or tools. | [Official pricing](https://learn.chatgpt.com/docs/pricing#how-much-does-sites-cost) says included; it does not give a Sites storage rate card. |
| This account's remaining capacity, credits or automatic overage behavior? | Unknown. The read-only `sites_get_site` result confirms an active site but exposes no billing, usage, storage or quota fields. No available native Sites tool supplies these figures. | Actual `mcp__codex_apps__sites_get_site` call using the project ID copied from the local hosting manifest. No credentials or account details retained in this report. |

The current [Sites overview](https://learn.chatgpt.com/docs/sites) directs readers to the Help Center for current pricing, plans and usage limits. The fetched official Learn pages and the Site's native inspection tools do not establish account-specific headroom or a promise that every storage size stays included. No upload was attempted to discover a limit.

The [storage guide](https://learn.chatgpt.com/docs/sites/data-and-connections) identifies R2 as file storage. The installed Sites skill's `references/storage.md` explains provisioning and bindings but supplies no storage pricing or quota. The repository hosting README's earlier “No paid hosting service is used” is a project note, not a billing guarantee. Cloudflare retail R2 rates and the OpenAI API's file-search rates are not evidence of Sites charges.

Decision: **included beta hosting is verified; unlimited free storage at any size is not verified.** The lean-data measurement can guide a later upload decision, but no cost boundary or storage purchase has been authorised.
