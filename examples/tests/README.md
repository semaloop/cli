# Code-managed tests

Each Markdown file is one Semaloop test. The frontmatter holds its metadata and the body is the test, written in natural language.

| Field    | Required | Meaning                                                                 |
| -------- | -------- | ----------------------------------------------------------------------- |
| `id`     | Yes      | Stable identifier, unique within the app. Renaming the file keeps the test as long as the `id` stays the same. |
| `name`   | Yes      | Name shown in Semaloop.                                                 |
| `folder` | No       | Folder name. Created if it doesn't exist.                               |
| `draft`  | No       | Drafts don't run automatically on new builds.                           |

```
semaloop test sync --dry-run examples/tests   # show what would change
semaloop test sync examples/tests              # apply it
```

A sync makes Semaloop match the files: it creates new tests, updates changed ones and archives any code-managed test whose file is gone. Tests created in the web dashboard are never touched, and code-managed tests can't be edited there.
