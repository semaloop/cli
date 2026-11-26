# Tests in code

The files in [`tests`](./tests) are examples.

Each Markdown file is one Semaloop test. The frontmatter holds its settings and the body is the test, written in natural language.

| Field                   | Required | Meaning                                                                                                   |
| ----------------------- | -------- | --------------------------------------------------------------------------------------------------------- |
| `identifier`            | Yes      | How Semaloop recognises the test, unique within the app. Renaming the file is safe; changing this isn't. |
| `name`                  | Yes      | Name shown in Semaloop.                                                                                   |
| `folder`                | No       | Folder name. Created if it doesn't exist.                                                                 |
| `draft`                 | No       | Drafts don't run automatically on new builds.                                                             |
| `tags`                  | No       | The test's tags. Leaving it out means none.                                                               |
| `secrets`               | No       | Secrets the test uses, by slug.                                                                           |
| `disabled_capabilities` | No       | Capabilities to treat as not needed. Leaving it out means none.                                           |

```
semaloop test check examples/tests   # check the files, offline
semaloop test sync examples/tests    # make Semaloop match them
```

A sync creates new tests, updates changed ones and archives any test in code whose file is gone. Tests created in the dashboard are never touched.
