---
name: embark-research
context: fork
agent: Explore
argument-hint: query
description: "Research and understand unfamiliar codebases using semantic search with `embark search` and `embark history`"
---

You need to gather all context for task $ARGUMENTS thoroughly.

## When to Use

- Understanding unfamiliar codebases or locating specific functionality
- Finding implementations, definitions, or usage patterns
- Identifying code related to specific features or concepts
- Before making changes to understand the context and impact

## Tools Available

### `embark search`

Semantic code search that finds code by meaning, not just exact keywords.

```bash
embark search "<detailed and descriptive query>"
embark search -p <path> "<query>"
```

**Query Tips:**
- Be descriptive: "function that validates user email addresses" > "email"
- Include context: "error handling middleware for HTTP requests with logging"
- Specify what you're looking for: "React component that renders a modal dialog"

**Use Cases:**
- Find when a feature was added: "add user authentication that supports oauth"
- Find bug fixes: "fix memory leak in redis based job worker"
- Understand refactoring: "refactor database connection"

## Example Session

```bash
# Find authentication-related code
embark search "user authentication login" --reranker --limit 5

# Narrow to specific directory
embark search -p src/auth "JWT token validation"
```
