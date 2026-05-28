// LLMFox demo data — multi-session, multi-turn coding agent with tool calls + cache

const SYSTEM_PROMPT = `You are an autonomous coding agent operating inside a sandboxed workspace.
You have access to the following tools: read_file, write_file, run_tests, search_code, run_shell.
Follow the workspace's existing style. Prefer small, testable diffs.
Never delete user data. Ask before destructive operations.
When you finish a task, summarize the changes and any follow-ups.`;

const PROJECT_CONTEXT = `<project>
  <name>checkout-service</name>
  <language>python 3.11</language>
  <framework>fastapi + sqlalchemy</framework>
  <test_runner>pytest</test_runner>
</project>`;

// ---- helpers --------------------------------------------------------------
const tok = (p, c, hit, miss) => ({ prompt: p, completion: c, cacheHit: hit, cacheMiss: miss });
const cost = (t) => ((t.cacheHit * 0.0000003) + (t.cacheMiss * 0.000003) + (t.completion * 0.000015));

// A "segment" is a labeled chunk of the prompt with cache status.
// status: 'hit' = served from cache, 'new' = sent fresh this turn, 'evicted' = was cached, no longer.
const seg = (label, text, status, hits = 1) => ({ label, text, status, hits });

// ---- Session 1: Coding agent — refactor auth module -----------------------
const session1 = {
  id: 's-7f2a',
  title: 'Refactor auth.verify_token',
  project: 'checkout-service',
  model: 'claude-sonnet-4-5',
  startedAt: '2026-05-25T09:14:22Z',
  duration: 48_220,
  status: 'completed',
  totalTokens: 18_412,
  cachedTokens: 14_088,
  cost: 0.0742,
  turns: [
    {
      id: 't1',
      kind: 'user',
      time: '09:14:22',
      tokens: tok(2148, 0, 0, 2148),
      content: 'The auth.verify_token function is doing too much. Split signature verification, expiry check, and claims parsing into separate pure functions. Keep the public signature backward compatible.',
      segments: [
        seg('system', SYSTEM_PROMPT, 'new'),
        seg('project_context', PROJECT_CONTEXT, 'new'),
        seg('user_message', 'The auth.verify_token function is doing too much. Split signature verification, expiry check, and claims parsing into separate pure functions. Keep the public signature backward compatible.', 'new'),
      ],
    },
    {
      id: 't2',
      kind: 'assistant',
      time: '09:14:24',
      duration: 2_140,
      tokens: tok(2148, 312, 0, 2148),
      breakpoint: false,
      content: "I'll start by reading the current implementation and its tests.",
      streaming: [
        "I'll", " start", " by", " reading", " the", " current", " implementation",
        " and", " its", " tests", ".", "\n\n", "Let", " me", " open", " auth.py",
        " first", " so", " I", " can", " see", " what", " verify_token", " is",
        " doing", " today", "."
      ],
      toolCalls: [
        {
          id: 'tc-01',
          name: 'read_file',
          args: { path: 'src/auth.py' },
          duration: 12,
          status: 'ok',
          result: `from datetime import datetime, timezone
import jwt
from .config import settings
from .errors import AuthError

def verify_token(token: str) -> dict:
    try:
        payload = jwt.decode(token, settings.public_key,
                             algorithms=['RS256'], options={'verify_exp': False})
    except jwt.InvalidSignatureError:
        raise AuthError('bad_signature')
    if payload.get('exp', 0) < datetime.now(timezone.utc).timestamp():
        raise AuthError('expired')
    if 'sub' not in payload or 'scope' not in payload:
        raise AuthError('missing_claims')
    return payload`,
        },
        {
          id: 'tc-02',
          name: 'read_file',
          args: { path: 'tests/test_auth.py' },
          duration: 9,
          status: 'ok',
          result: `import pytest
from src.auth import verify_token
from src.errors import AuthError

def test_valid_token(signed_token):
    payload = verify_token(signed_token)
    assert payload['sub'] == 'user_42'

def test_expired_token(expired_token):
    with pytest.raises(AuthError, match='expired'):
        verify_token(expired_token)

def test_bad_signature(tampered_token):
    with pytest.raises(AuthError, match='bad_signature'):
        verify_token(tampered_token)`,
        },
      ],
    },
    {
      id: 't3',
      kind: 'assistant',
      time: '09:14:31',
      duration: 3_980,
      tokens: tok(3892, 524, 2148, 1744),
      breakpoint: true,
      content: `Here's my plan:

1. Extract \`_decode_unverified(token)\` — pure jwt.decode wrapper, raises on bad signature.
2. Extract \`_check_expiry(payload, now=None)\` — pure, takes a clock for testability.
3. Extract \`_require_claims(payload, required=('sub','scope'))\` — pure validator.
4. \`verify_token(token)\` composes the three. Signature unchanged.

Proceed?`,
      segments: [
        seg('system', SYSTEM_PROMPT, 'hit', 14),
        seg('project_context', PROJECT_CONTEXT, 'hit', 14),
        seg('turn_1_user', '<user msg #1>', 'hit', 12),
        seg('turn_2_assistant', '<assistant msg #2 + tool calls>', 'hit', 8),
        seg('tool_result_1', '<read_file result: src/auth.py>', 'hit', 4),
        seg('tool_result_2', '<read_file result: tests/test_auth.py>', 'new'),
      ],
    },
    {
      id: 't4',
      kind: 'user',
      time: '09:14:48',
      tokens: tok(4416, 0, 3892, 524),
      content: 'Go ahead. Make sure the existing tests still pass.',
    },
    {
      id: 't5',
      kind: 'assistant',
      time: '09:14:50',
      duration: 8_204,
      tokens: tok(4416, 1248, 3892, 524),
      breakpoint: false,
      content: 'Writing the refactor now.',
      toolCalls: [
        {
          id: 'tc-03',
          name: 'write_file',
          args: { path: 'src/auth.py', mode: 'overwrite' },
          duration: 18,
          status: 'ok',
          result: 'wrote 38 lines',
        },
        {
          id: 'tc-04',
          name: 'run_tests',
          args: { path: 'tests/test_auth.py' },
          duration: 1_412,
          status: 'fail',
          result: `============================= test session starts ==============================
collected 3 items

tests/test_auth.py .F.                                                   [100%]
=================================== FAILURES ===================================
________________________ test_expired_token ____________________________________
    with pytest.raises(AuthError, match='expired'):
>       verify_token(expired_token)
E       AuthError: missing_claims
============================== 1 failed, 2 passed ==============================`,
        },
        {
          id: 'tc-05',
          name: 'read_file',
          args: { path: 'src/auth.py' },
          duration: 8,
          status: 'ok',
          result: '(38 lines — order of checks was wrong: expiry checked AFTER claims)',
        },
        {
          id: 'tc-06',
          name: 'write_file',
          args: { path: 'src/auth.py', mode: 'patch', hunks: 1 },
          duration: 14,
          status: 'ok',
          result: 'patched 1 hunk',
        },
        {
          id: 'tc-07',
          name: 'run_tests',
          args: { path: 'tests/test_auth.py' },
          duration: 1_388,
          status: 'ok',
          result: '3 passed in 0.42s',
        },
      ],
    },
    {
      id: 't6',
      kind: 'assistant',
      time: '09:15:02',
      duration: 1_840,
      tokens: tok(5664, 186, 4416, 1248),
      content: `Done. \`verify_token\` is now a 4-line composition over three pure helpers. All three existing tests pass. One follow-up: \`_check_expiry\` accepts an injectable clock — worth adding a freezegun test.`,
    },
    {
      id: 't7',
      kind: 'user',
      time: '09:15:33',
      tokens: tok(5850, 0, 5664, 186),
      content: 'What if exp is missing entirely? Should that be treated as expired or missing_claims?',
    },
    {
      id: 't8',
      kind: 'assistant',
      time: '09:15:34',
      duration: 1_320,
      tokens: tok(5850, 412, 5664, 186),
      content: `Right now \`_check_expiry\` does \`payload.get('exp', 0) < now\`, so a missing \`exp\` reads as "expired in 1970" → AuthError('expired'). That preserves the previous behavior but it's misleading.

Recommendation: treat missing \`exp\` as \`missing_claims\` and add \`exp\` to the required set. Want me to apply that?`,
    },
  ],
};

// ---- Session 2: RAG question with long cached system prompt ---------------
const session2 = {
  id: 's-1c4e',
  title: 'RAG: pricing rules lookup',
  project: 'support-bot',
  model: 'claude-sonnet-4-5',
  startedAt: '2026-05-25T09:02:11Z',
  duration: 3_120,
  status: 'completed',
  totalTokens: 9_842,
  cachedTokens: 8_104,
  cost: 0.0188,
  turns: [
    {
      id: 't1',
      kind: 'user',
      time: '09:02:11',
      tokens: tok(8104, 0, 8104, 0),
      content: 'Does the volume discount apply to add-ons or only the base subscription?',
    },
    {
      id: 't2',
      kind: 'assistant',
      time: '09:02:12',
      duration: 1_412,
      tokens: tok(8104, 248, 8104, 0),
      content: 'Per the pricing rules doc §4.2: volume discounts apply to the base subscription only. Add-ons are billed at list price regardless of seat count. (Source: pricing/rules-v3.md, lines 88–94.)',
      toolCalls: [
        { id: 'tc-r1', name: 'search_docs', args: { q: 'volume discount add-ons', k: 4 }, duration: 184, status: 'ok', result: '4 hits — top: pricing/rules-v3.md#4.2' },
      ],
    },
  ],
};

// ---- Session 3: failed, with an error -------------------------------------
const session3 = {
  id: 's-9b03',
  title: 'Migrate to async pg driver',
  project: 'checkout-service',
  model: 'claude-sonnet-4-5',
  startedAt: '2026-05-25T08:41:07Z',
  duration: 12_400,
  status: 'failed',
  totalTokens: 6_220,
  cachedTokens: 2_148,
  cost: 0.0411,
  turns: [
    {
      id: 't1', kind: 'user', time: '08:41:07', tokens: tok(2148, 0, 0, 2148),
      content: 'Switch the db layer from psycopg2 to asyncpg without changing the public ORM API.',
    },
    {
      id: 't2', kind: 'assistant', time: '08:41:09', duration: 4_120,
      tokens: tok(2148, 488, 0, 2148),
      content: 'Scanning the data layer first.',
      toolCalls: [
        { id: 'tc-e1', name: 'search_code', args: { q: 'psycopg2', kind: 'symbol' }, duration: 220, status: 'ok', result: '14 references across 8 files' },
        { id: 'tc-e2', name: 'read_file', args: { path: 'src/db/session.py' }, duration: 11, status: 'ok', result: '(89 lines)' },
        { id: 'tc-e3', name: 'run_shell', args: { cmd: 'poetry add asyncpg' }, duration: 8_104, status: 'error', result: 'ResolverProblemError: asyncpg 0.29 requires Python >=3.8,<3.13; current 3.13.0' },
      ],
    },
    {
      id: 't3', kind: 'assistant', time: '08:41:22', duration: 1_120,
      tokens: tok(2636, 174, 2148, 488),
      error: 'aborted: dependency resolution failed',
      content: 'Aborting: asyncpg 0.29 does not yet support Python 3.13. Suggest pinning to 3.12 in pyproject or waiting for asyncpg 0.30.',
    },
  ],
};

const SESSIONS = [session1, session2, session3];

// ---- variables in scope (for the "Variables" debug panel) -----------------
const VARIABLES_BY_TURN = {
  't3': [
    { name: 'model', type: 'string', value: '"claude-sonnet-4-5"' },
    { name: 'temperature', type: 'number', value: '0.2' },
    { name: 'max_tokens', type: 'number', value: '4096' },
    { name: 'tools', type: 'array(5)', value: '[read_file, write_file, run_tests, search_code, run_shell]', expandable: true },
    { name: 'context_window', type: 'object', value: '{ used: 5,664 / 200,000 (2.8%) }' },
    { name: 'cache_breakpoints', type: 'array(2)', value: '[after_system, after_turn_2]' },
    { name: 'stop_sequences', type: 'array(0)', value: '[]' },
  ],
};

// ---- raw API payloads (for the Request / Response tabs) -------------------
const RAW_REQUEST_T3 = `POST /v1/messages HTTP/1.1
host: api.anthropic.com
anthropic-version: 2026-04-01
content-type: application/json
anthropic-beta: prompt-caching-2025-01-15

{
  "model": "claude-sonnet-4-5",
  "max_tokens": 4096,
  "temperature": 0.2,
  "system": [
    { "type": "text", "text": "You are an autonomous coding agent...",
      "cache_control": { "type": "ephemeral" } }
  ],
  "tools": [
    { "name": "read_file", "input_schema": { ... } },
    { "name": "write_file", "input_schema": { ... } },
    { "name": "run_tests", "input_schema": { ... } },
    { "name": "search_code", "input_schema": { ... } },
    { "name": "run_shell", "input_schema": { ... } }
  ],
  "messages": [
    { "role": "user",      "content": "<turn 1 user msg>" },
    { "role": "assistant", "content": [
        { "type": "text", "text": "I'll start by reading..." },
        { "type": "tool_use", "id": "tc-01", "name": "read_file", "input": { "path": "src/auth.py" } },
        { "type": "tool_use", "id": "tc-02", "name": "read_file", "input": { "path": "tests/test_auth.py" } }
    ]},
    { "role": "user", "content": [
        { "type": "tool_result", "tool_use_id": "tc-01", "content": "<file contents>" },
        { "type": "tool_result", "tool_use_id": "tc-02", "content": "<file contents>",
          "cache_control": { "type": "ephemeral" } }
    ]}
  ]
}`;

const RAW_RESPONSE_T3 = `HTTP/1.1 200 OK
content-type: application/json
anthropic-request-id: req_01HXY7K2Q9MN3PVR8...

{
  "id": "msg_01ABc...",
  "type": "message",
  "role": "assistant",
  "model": "claude-sonnet-4-5",
  "stop_reason": "end_turn",
  "usage": {
    "input_tokens": 1744,
    "cache_creation_input_tokens": 1744,
    "cache_read_input_tokens": 2148,
    "output_tokens": 524
  },
  "content": [
    { "type": "text", "text": "Here's my plan:\\n\\n1. Extract _decode_unverified(token)..." }
  ]
}`;

// ---- diff between turn 3 and a replayed alternative ------------------------
const DIFF_T3 = [
  { kind: 'ctx',  text: "Here's my plan:" },
  { kind: 'ctx',  text: '' },
  { kind: 'del',  text: '1. Extract _decode_unverified(token) — pure jwt.decode wrapper, raises on bad signature.' },
  { kind: 'add',  text: '1. Extract decode_jwt(token) — thin wrapper that re-raises as AuthError("bad_signature").' },
  { kind: 'ctx',  text: '2. Extract _check_expiry(payload, now=None) — pure, takes a clock for testability.' },
  { kind: 'del',  text: '3. Extract _require_claims(payload, required=(\'sub\',\'scope\')) — pure validator.' },
  { kind: 'add',  text: "3. Extract require_claims(payload, required=('sub','scope','exp')) — note 'exp' now required." },
  { kind: 'ctx',  text: '4. verify_token(token) composes the three. Signature unchanged.' },
  { kind: 'ctx',  text: '' },
  { kind: 'ctx',  text: 'Proceed?' },
];

window.LLMFOX_DATA = {
  SESSIONS,
  VARIABLES_BY_TURN,
  RAW_REQUEST_T3,
  RAW_RESPONSE_T3,
  DIFF_T3,
  cost,
};
