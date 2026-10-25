# Contributing

Issues and pull requests are welcome. Keep a pull request to one change and
say what it fixes or adds, with the commands you ran; a UI change needs a
screenshot.

Setup and commands are in the [README](README.md#development). Before pushing:

```sh
make check test
```

Commit titles follow `type(scope): summary`, for example
`fix(scan): keep clips when the share is offline`. They become the release
notes.

Ground rules of the project:

- Never write to the recordings folder. Downloads serve the original bytes.
- No transcoding cache: anything derived from a clip at play time is built in
  memory per request.
- It must stay light enough for a 2-core, 1 GB container.
