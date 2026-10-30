# Application review for 3.2.10

Reviewed current main against published executable v3.2.9, preserving the original v3.0.10 collector baseline. Scope: daily collection setup, progress/log inspection, report access, accessibility, window sizing, settings and release/update compatibility.

| Finding | Implemented behavior |
| --- | --- |
| Category CSS referenced undefined color tokens, leaving weak selection boundaries. | Defined shared colors, visible selected cards and clearer profile buttons. |
| An enabled export silently reselected an inventory category after the user unchecked it. | Deselecting inventory clears its dependent export. Selecting an export enables its matching inventory. |
| Only the output folder was remembered. | Save choices and collection start retain the safe scope, output folder and event-history days in Settings/collection.json. |
| Thirty-four categories and thirteen exports were difficult to scan. | Category/description search, visible counts, scope summary and expandable exports. Search never changes selections. |
| Every log poll forced the view to the end. | Scrolling back pauses live following. Search and severity filters retain the complete received log. |
| Bad inputs and ordinary request failures opened blocking alerts. | Inline errors keep the current choices and existing log intact. Explicit install and sensitive-export confirmations remain. |
| Event-history days could remain editable during collection. | All collection inputs and Save/Start actions stay locked while busy. |
| Tabs lacked keyboard/selection semantics, and small windows cramped the fixed sidebar. | Arrow/Home/End tab navigation, selection attributes, focus indicators, responsive server strip and persistent action buttons. |

No new role collectors, unattended collection, comparison engine, remote service, browser extension or runtime dependency was introduced. Original report generation and data parity remain under existing provenance checks.

Validation uses the exact v3.2.9 HTML as a negative control for missing card borders, forced checkbox reselection, log scroll jumps and the event-history lock. Browser checks use actual application HTML and the production CSP with synthetic state; they are not live server collection. Native Windows checks save through the built executable's authenticated endpoint, close/reopen its real window and read restored choices. Published upgrade validation performs the real v3.2.9 update and verifies saved scope after a shortcut relaunch. See DEVELOPMENT_HANDOFF.md and docs/VALIDATION.md for final results and limits.
