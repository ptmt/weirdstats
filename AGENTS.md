Notes
- Strava access tokens are short-lived; use refresh tokens with STRAVA_CLIENT_ID/STRAVA_CLIENT_SECRET and STRAVA_REFRESH_TOKEN. The app stores tokens in SQLite and refreshes when expired.
- Keep secrets in `.env` and do not commit them.
- Token storage currently assumes a single user (user_id = 1).

<!-- embark-instructions-start -->
# Tools

## Semantic Code Search (EmbArk)

You have access to the EmbArk MCP `code_search` tool for searching the codebase semantically.
This tool can search for code snippets related in meaning to the search query and search objective.

### When to use semantic search:
- Understanding unfamiliar codebases or locating specific functionality.
- Finding implementations, definitions, or usage patterns.
- Identifying code related to specific features or concepts.
- Before making changes to understand the context and impact.

Use this tool proactively when you need to understand code structure or locate relevant implementations.

<!-- embark-instructions-end -->