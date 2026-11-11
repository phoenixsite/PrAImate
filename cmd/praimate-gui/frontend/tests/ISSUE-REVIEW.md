# Worker and Documents UI regression checks

`npm run test:studio` includes functional Documents session tests. Worker board
and plan handlers can be checked with:

```sh
node --test src/lib/workerBoard.test.js src/lib/WorkerGraph.test.js
```

The optional browser check uses real Svelte components and Studio styles with a
synthetic backend, without accessing accounts or creating real sessions. Start
Vite and a separate Firefox profile in two terminals:

```sh
npm run dev -- --host 127.0.0.1 --port 5188 --force
```

```sh
mkdir -p /tmp/praimate-review-firefox
firefox --headless --no-remote --new-instance --remote-debugging-port=9235 --profile /tmp/praimate-review-firefox about:blank
```

Then run `node tests/issue-review.mjs`. It checks primary-button hover in both
themes and Documents MCP selection before opening an editor. `BIDI_URL`,
`REVIEW_TEST_URL` and `REVIEW_TEST_OUTPUT` override the connection and screenshot
paths. Run one Vite server at a time if worktrees share `node_modules`, since
their dependency optimization caches also share that directory.
