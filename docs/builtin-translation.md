# Built-in Translation

Status: design note. No translation code exists in this source. This document records where a dictionary-driven translation layer fits, which existing surfaces it reuses, and which decisions stay open.

## Why substitution belongs inside the engine

The runtime turns game text into pixels itself, and the string is a Go value before that point. A translated string can replace the source string upstream of the game's own layout, so wrapping, centering, ruby, and textbox fitting are computed from the translation by the same code path that handled the original.

An external tool has to reach the drawn string and the font metric functions separately: the reference clone `reference/LunaTranslator` hooks `tTVPNativeBaseBitmap::DrawText` and writes the translated text back into the game's buffer (`src/NativeImpl/LunaHook/LunaHook/engine32/KiriKiri.cpp`), and additionally hooks `GetTextExtentPoint32W`/`GetGlyphOutlineW` through `embed_hook_font` so the game re-measures it. Its own documentation (`docs/zh/embedtranslate.md`) records the resulting costs: font and encoding repair, per-line character limits, and game stalls while a slow translator is awaited.

## Text surfaces in this source

1. **Scenario tokens.** `KAGParser.LoadScenario` ([`internal/runtime/kag_execution.go:832`](../internal/runtime/kag_execution.go)) calls `kag.Parse` ([`internal/kag/parser.go:122`](../internal/kag/parser.go)), which returns a `kag.Scenario` whose `Tokens` carry `Token.Text` for `KindText`, `Token.Tag.Attributes[].Value` for command arguments, and `Token.Label.Caption` for labels. The `OnScenarioLoad` callback ([`kag_execution.go:148`](../internal/runtime/kag_execution.go)) already allows source-level replacement before parsing, and the native bridge builds it at [`internal/runtime/kag_native.go:43`](../internal/runtime/kag_native.go).
2. **Draw entry points.** `Services.layerDrawText` ([`internal/runtime/layer_native.go:1479`](../internal/runtime/layer_native.go)), `Services.imageDrawText` ([`internal/runtime/image_function_native.go:1072`](../internal/runtime/image_function_native.go)), and `Services.textRenderRender` ([`internal/runtime/text_render_native.go:349`](../internal/runtime/text_render_native.go)). `TextRenderBase` also exposes the assembled buffer as `renderText` ([`text_render_native.go:670`](../internal/runtime/text_render_native.go)).
3. **Font metrics.** `Font.getTextWidth`, `getTextHeight`, and the four escapement methods ([`internal/runtime/font_native.go:300`](../internal/runtime/font_native.go)). These are what game scripts use to place and wrap text, so they measure whatever string reaches them.

## Primary design: token-level substitution

Transform the parsed scenario between `kag.Parse` and `kag.NewParser` inside `KAGParser.LoadScenario`. Proposed shape: a `Services` field holding a translation table plus a `translateScenario(kag.Scenario) kag.Scenario` step.

Rules:

- Rewrite `Token.Text` for `KindText` tokens that have a table entry.
- Rewrite `Tag.Attributes[].Value` only for an allowlist of tags, for example the displayed-name attribute and choice captions. Structural values stay as written: storage names, label names, macro arguments, and expression attributes marked by `Attribute.Entity` ([`internal/kag/token.go:13`](../internal/kag/token.go)).
- Leave `Token.Kind`, `Token.Location`, `Token.Label.Name`, and `Token.InlineScript` untouched. Line reporting and macro collection depend on them.
- Keep the mapping one source token to one target token.

Save compatibility follows from `kag.Cursor.Position` being a token index ([`internal/kag/token.go:224`](../internal/kag/token.go)) that KAG `store` reports as `position` ([`internal/runtime/kag_native.go:409`](../internal/runtime/kag_native.go)). In-place rewrites leave every index valid. Inserting or dropping tokens would need a remap table.

Layout consequences:

- Wrapping, alignment, and ruby are recomputed by the game's own message layer from the translated string through the engine's `Font`.
- Where a line must fit a fixed box, the substitution step can pre-measure candidate strings with the same `Font` and insert breaks, or adjust `fontScale`/`timeScale` ([`internal/runtime/session.go:359`](../internal/runtime/session.go)).
- Character reveal timing is observable: `TextRenderBase.render` handles `\n`, `\k`, `\t`, and `\x` escapes and records key waits ([`text_render_native.go:414`](../internal/runtime/text_render_native.go)), and `renderCount` is exposed. A per-line entry can carry its own timing so the reveal matches the voice track.

## Secondary design: draw-level exact-string dictionary

Some text never passes through the scenario: captions and menu labels defined in the game's own `.tjs` system scripts.

- Hook point: before rasterization inside `layerDrawText` and `textRenderRender`.
- Matching: exact string after normalization (trim, NFKC), with a per-entry flag distinguishing labels from names.
- Coverage: short fixed strings, terminology, and speaker-name variants.
- Limit: KAG message layers call `Layer.drawText` once per character (fixture example: [`testdata/lovelyday/data/sys_source/MessObject.tjs:1767`](../testdata/lovelyday/data/sys_source/MessObject.tjs)), so sentences are not available at this layer. Sentence translation belongs to the token-level path.

Both layers share one table. The token-level path covers scenario dialogue, choices, and history; the draw-level path covers the remaining fixed UI strings.

## Table format and supply

- Keys: hash of the source string, with the source string retained for review. Values: target string plus optional metadata (kind, maximum width, break positions, reveal timing).
- Build: extract every unique source string offline, translate in batches with per-scene context and a shared glossary, then ship the table. One entry per unique source keeps terminology identical across the game.
- Runtime: lookup only. Unknown entries pass through unchanged. The render path performs no network calls and awaits no external service, so text reveal and voice timing keep their original pacing.
- Placement: read through `Services.readStorage` ([`internal/runtime/services.go:628`](../internal/runtime/services.go)) or from a path beside the project. The launcher already resolves project and save directories through `core.SaveDirectory` ([`internal/core/project.go:16`](../internal/core/project.go)).

## Extraction and readback

- `krkrgo-tool extract` currently unpacks XP3 members only ([`cmd/krkrgo-tool/main.go:58`](../cmd/krkrgo-tool/main.go)). A translation extraction step would run the same KAG parse over `.ks`, `.tjs`, and `.csv` members and emit the unique string table with token locations.
- `krkrgo-tool replay` ([`cmd/krkrgo-tool/main.go:152`](../cmd/krkrgo-tool/main.go)) with [`internal/host/replay.go`](../internal/host/replay.go) gives a deterministic run. A translated run and the source run can be diffed over emitted text tokens and draw strings.

## Encoding and fonts

VM strings are UTF-16 internally ([`internal/tjs/utf16.go`](../internal/tjs/utf16.go)), so target languages need no Shift-JIS conversion. Font selection still depends on the faces resolved by [`internal/runtime/font_system.go`](../internal/runtime/font_system.go); a target language needs a face covering its characters on the host system.

## Open items

- Token-count stability: one-to-one substitution preserves saves. Any other mapping needs an explicit remap.
- Fixed-width fields where the game hardcodes a character count still need per-entry width metadata, because the game's own wrap loop is bypassed for those fields.
- Strings with no character form stay out of reach: text baked into images (PNG/TLG/PSB), decoded video frames, and PSB motions. These need asset replacement or OCR.
- Unimplemented native methods skip drawing entirely; for example `Font.getGlyph` has no implementation in this source, so games using that path render nothing regardless of translation.
- Games whose own TJS implements the scenario parser reach this path only when they use the native `KAGParser` class (fixture example: [`testdata/lovelyday/data/sys_source/Parser.tjs:22`](../testdata/lovelyday/data/sys_source/Parser.tjs) extends `KAGParser`). Otherwise the substitution point moves to `Scripts` and storage reads.
