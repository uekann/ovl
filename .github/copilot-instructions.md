# Pull request review guidance

- Apply the `## Code Review Rules` in the root `AGENTS.md` to every review. Prioritize correctness, data-loss risks, credential exposure, and compatibility with Overleaf's private APIs.
- Report only actionable findings introduced by the pull request. Include the failure scenario and point to the smallest relevant line range.
- Do not repeat formatting, module consistency, lint, build, or test results that the required `CI required` check already enforces.
- Flag any change that weakens, skips, or makes conditional the required format, module, lint, build, test, race-test, or current-commit LLM-review gates.
