---
name: embark-search
description: "Research and understand unfamiliar codebases using semantic code search"
---

# EmbArk Semantic Code Search

Use `embark search` to find code by meaning, not just keywords.

## Usage

```bash
embark search "<detailed and descriptive query>"
embark search -p <path> "<query>"
```

## Query Tips

- Be descriptive: "function that validates user email addresses" > "email"
- Include context: "error handling middleware for HTTP requests with logging"
- Specify what you're looking for: "React component that renders a modal dialog"

## Examples

```bash
# Find authentication-related code
embark search "user authentication login flow"

# Narrow to specific directory
embark search -p src/auth "JWT token validation"

# Get more results
embark search --limit 10 "database connection pooling configuration"
```
