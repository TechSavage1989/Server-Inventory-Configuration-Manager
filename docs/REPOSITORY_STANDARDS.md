# Repository standards

These rules apply to every TechSavage1989 app repository and to **every contributor, human or AI**: Claude, ChatGPT/Codex, Copilot, Cursor, Gemini or any other tool. The file is identical in every app repository so the projects stay organized the same way. This repository's `AGENTS.md` adds project-specific rules; where the two conflict, the project-specific rule wins, and an enforced check script (layout, root or docs check) wins over both.

Read first, in this order: `AGENTS.md`, `DEVELOPMENT_HANDOFF.md` (where present), then this file.

## 1. README

The README is the project's front page: concise, approachable and user-first. Explain what the app does, how to get it and how to use it before any developer detail. Keep each app's accurate features, limitations and safety cautions; never copy claims from another app or invent features, versions or results.

**Header**, centered:
- The app's real icon or logo at about 112 px wide, if one exists (never invent branding or a placeholder).
- Exactly one `<h1>` with the app name, then a one-sentence tagline.
- A quick-link row in this order: **Download · Get started · Features · Screenshots · Updates · Development**. Each links to its section; drop a link only when its section genuinely doesn't apply. For a web app, *Open the app* may replace *Download*.
- The hero screenshot, `docs/images/hero.png`, at `width="900"`.

**Sections**, in this order, with these names (leave out any that don't apply; put project-specific sections after *How to use*):
`Features` · `Screenshots` · `Download` · `Requirements` · `Getting started` · `How to use` · `Updates and settings` · `Troubleshooting` · `Development` · `License`

- **Screenshots:** 2–4 images, each with a bold one-line caption above it.
- **Download:** link the stable latest release only when a stable release exists; for previews and prereleases link the releases index. Never link a stale or guessed asset.
- **Development:** link `AGENTS.md` and `DEVELOPMENT_HANDOFF.md`; keep long architecture notes in `docs/`.
- **License:** only when the repository really has one.

Update the README in the same pull request whenever features, workflows, requirements, install steps, settings or update behavior change. Badges are optional, and only for verified claims.

## 2. Screenshots and images

- All README imagery lives in **`docs/images/`**: `hero.png` plus `<short-kebab-name>.png` for the rest. PNG, ideally under 400 KB each.
- Screenshots must be **real captures of the current app**, never mockups or edited fakes. If the UI can't be captured honestly, show real output from the app's own code, or leave the image out.
- Use **fictional sample data** only. No real people, companies, accounts, hostnames or IPs (use `192.0.2.x`, `10.x`, `example.com`), no media titles or copyrighted artwork, no secrets.
- **When a change visibly alters the UI, update the affected screenshots in the same pull request.** A stale screenshot is a bug.
- Keep capture scripts and scratch files out of the repository unless the project already keeps them under `scripts/` or `tests/`.
- If a source-audit or binary allowlist check exists, it must allow `docs/images/*.png`.

## 3. Layout and root

- Keep the root compact: `README.md`, `AGENTS.md`, `DEVELOPMENT_HANDOFF.md`, `CLAUDE.md`, required manifests and build configuration, the license, and small build-entry wrappers only where the workflow needs them. Hidden configuration such as `.github/` stays where it conventionally lives.
- Group implementation in the platform's natural structure (`src/`, `cmd/` + `internal/`, `app/`, `web/`, …) and supporting material in `docs/`, `docs/releases/`, `docs/images/`, `scripts/`, `tests/`, `assets/` and `vendor/`, reusing sensible existing equivalents. Don't create empty folders to match this list.
- Where a tracked-root allowlist and CI check exist, keep them. A genuinely required new root file is added to the allowlist in the same reviewed change, with the reason recorded in `AGENTS.md`.
- **Never commit** build output, installers, binaries or archives you produced, source ZIPs, logs, caches, `node_modules`, virtual environments, secrets, `.env` files, dated temporary reports, old handoffs, or OS junk (`.DS_Store`, `Thumbs.db`). Extend `.gitignore` instead. Vendored files that the build depends on stay where the build expects them, with their notices.
- Before moving anything, map every reference to it (build commands, CI, embeds, resources, fixtures, tests, archive manifests, docs links, image paths) and update them together. Never move files that another developer or agent is actively changing.

## 4. Branches, pull requests and versions

- Never commit straight to `main`. Use a short-lived branch named for its purpose: `feat/…`, `fix/…`, `docs/…`, `chore/…`, `release/…`.
- One focused change per pull request. Say what changed and, for visible changes, what to look at.
- Merge only when the required checks are green; a pending or failed check is not success. **Delete the branch once it is merged**, and don't leave abandoned branches behind. Don't rewrite shared history or force-push.
- Follow the project's own versioning and release rules in `AGENTS.md`. Unless those rules say otherwise, **documentation-only or organization-only changes don't bump the version, tag, rebuild or publish anything**. Never overwrite a published release.

## 5. Agent instruction files

- `AGENTS.md` is the read-first guide and holds this project's knowledge; it links to this file. `CLAUDE.md` imports `AGENTS.md` so Claude reads the same instructions. Don't keep separate, diverging rule sets per tool.
- Update `DEVELOPMENT_HANDOFF.md` after meaningful changes: current version, build and test commands, known limitations, and any documentation or layout exception.
- When you learn something future contributors need (a rule, a gotcha, a command), add it to `AGENTS.md` in the same pull request.
- Change this file only to change the shared standard, and make the identical change in every app repository.
