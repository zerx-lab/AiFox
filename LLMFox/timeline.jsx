// Center: Timeline + Call stack views

const fmtMs = (ms) => ms == null ? '—' : ms < 1000 ? `${ms}ms` : `${(ms/1000).toFixed(2)}s`;
const fmtTok = (n) => n >= 1000 ? `${(n/1000).toFixed(1)}k` : String(n);

const TimelineCard = ({ turn, selected, onSelect, onSelectTool, selectedToolId, streamingTurnId, streamingProgress, onToggleBp }) => {
  const isAsst = turn.kind === 'assistant';
  const isUser = turn.kind === 'user';
  const role = isAsst ? 'assistant' : 'user';
  const isStreaming = streamingTurnId === turn.id;
  const visibleText = isStreaming && turn.streaming
    ? turn.streaming.slice(0, Math.floor(turn.streaming.length * streamingProgress)).join('')
    : turn.content;
  const ratio = turn.tokens ? (turn.tokens.cacheHit / Math.max(1, turn.tokens.cacheHit + turn.tokens.cacheMiss)) : 0;

  return (
    <div className="tl-row">
      <div className="tl-time">{turn.time}</div>
      <div className="tl-rail">
        <span className={'node ' + role}></span>
        <span
          className="bp-gutter"
          onClick={(e) => { e.stopPropagation(); onToggleBp(turn.id); }}
          title="Toggle breakpoint"
        ></span>
        {turn.breakpoint && <span className="tl-bp" title="Breakpoint" onClick={(e) => { e.stopPropagation(); onToggleBp(turn.id); }}></span>}
      </div>
      <div className="tl-content">
        <div
          className={'tl-card ' + role + (selected ? ' selected' : '')}
          onClick={() => onSelect(turn.id, null)}
        >
          <div className="tl-head">
            <span className={'role-chip ' + role}>{role}</span>
            <span style={{ color: 'var(--fg-2)' }}>{isUser ? 'human' : 'claude-sonnet-4-5'}</span>
            <div className="tl-meta">
              {turn.duration != null && <span>{fmtMs(turn.duration)}</span>}
              {turn.tokens && <span>{fmtTok(turn.tokens.prompt + turn.tokens.completion)} tok</span>}
              {turn.error && <span className="err">⚠ {turn.error}</span>}
            </div>
          </div>
          <div className={'tl-body' + (isStreaming ? ' streaming' : '')}>{visibleText}</div>

          {turn.toolCalls && turn.toolCalls.length > 0 && (
            <div className="tcalls" onClick={(e) => e.stopPropagation()}>
              {turn.toolCalls.map((tc) => (
                <div
                  key={tc.id}
                  className={'tcall' + (selectedToolId === tc.id ? ' selected' : '')}
                  onClick={() => onSelectTool(turn.id, tc.id)}
                >
                  <span className="tico"><Icon name="tool" size={12} /></span>
                  <span>
                    <span className="tname">{tc.name}</span>
                    <span className="targs">({Object.entries(tc.args).map(([k, v]) => `${k}: ${JSON.stringify(v)}`).join(', ')})</span>
                  </span>
                  <span style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                    <span style={{ color: 'var(--fg-4)', fontSize: 10 }}>{fmtMs(tc.duration)}</span>
                    <span className={'tstat ' + tc.status}>{tc.status}</span>
                  </span>
                </div>
              ))}
            </div>
          )}

          {turn.tokens && (
            <div className="tl-foot">
              {turn.tokens.cacheHit > 0 && (
                <span className="pill cached">cache hit {fmtTok(turn.tokens.cacheHit)}</span>
              )}
              {turn.tokens.cacheMiss > 0 && (
                <span className="pill new">new {fmtTok(turn.tokens.cacheMiss)}</span>
              )}
              {turn.tokens.completion > 0 && (
                <span className="pill">out {fmtTok(turn.tokens.completion)}</span>
              )}
              <span className="ratio"><i style={{ width: `${ratio * 100}%` }} /></span>
              <span className="pill cost">${(window.LLMFOX_DATA.cost(turn.tokens)).toFixed(4)}</span>
            </div>
          )}
        </div>
      </div>
    </div>
  );
};

const Timeline = ({ session, selection, onSelect, streamingTurnId, streamingProgress, onToggleBp }) => {
  return (
    <div className="tl">
      {session.turns.map((t) => (
        <TimelineCard
          key={t.id}
          turn={t}
          selected={selection.turnId === t.id && !selection.toolId}
          onSelect={(turnId) => onSelect(turnId, null)}
          onSelectTool={onSelect}
          selectedToolId={selection.turnId === t.id ? selection.toolId : null}
          streamingTurnId={streamingTurnId}
          streamingProgress={streamingProgress}
          onToggleBp={onToggleBp}
        />
      ))}
    </div>
  );
};

// ---- Call stack view (flat list of frames, with indent) -------------------
const CallStack = ({ session, selection, onSelect }) => {
  const frames = [];
  session.turns.forEach((t, ti) => {
    frames.push({
      kind: t.kind,
      id: t.id,
      depth: 0,
      label: t.kind === 'user' ? 'user.message' : 'assistant.respond',
      args: t.kind === 'user'
        ? `"${t.content.slice(0, 40)}${t.content.length > 40 ? '…' : ''}"`
        : `tokens=${t.tokens.prompt + t.tokens.completion}`,
      dur: t.duration,
      status: t.error ? 'fail' : 'ok',
      turnId: t.id,
    });
    if (t.toolCalls) {
      t.toolCalls.forEach((tc) => {
        frames.push({
          kind: 'tool',
          id: tc.id,
          depth: 1,
          label: tc.name,
          args: '(' + Object.entries(tc.args).map(([k, v]) => `${k}=${JSON.stringify(v)}`).join(', ') + ')',
          dur: tc.duration,
          status: tc.status === 'ok' ? 'ok' : 'fail',
          turnId: t.id,
          toolId: tc.id,
        });
      });
    }
  });

  return (
    <div className="cs">
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12, color: 'var(--fg-3)', fontSize: 11 }}>
        <Icon name="stack" size={12} />
        <span>Call stack — {frames.length} frames · {session.turns.length} turns</span>
      </div>
      {frames.map((f, i) => {
        const isSel =
          selection.turnId === f.turnId &&
          (f.kind === 'tool' ? selection.toolId === f.toolId : !selection.toolId);
        return (
          <div
            key={i}
            className={'cs-frame ' + f.kind + (isSel ? ' selected' : '')}
            style={{ paddingLeft: 8 + f.depth * 20 }}
            onClick={() => onSelect(f.turnId, f.toolId || null)}
          >
            <span className="ind">{f.depth > 0 ? <span className="cs-tree-rail">└─</span> : ''}</span>
            <span style={{ display: 'flex', alignItems: 'center', gap: 6, minWidth: 0, overflow: 'hidden' }}>
              <span className="ico">
                <Icon name={f.kind === 'tool' ? 'tool' : f.kind === 'user' ? 'user' : 'bot'} size={12} />
              </span>
              <span className="name">{f.label}</span>
              <span className="args" style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', minWidth: 0 }}>{f.args}</span>
            </span>
            <span className="dur">{fmtMs(f.dur)}</span>
            <span className={'stat ' + f.status}>{f.status}</span>
          </div>
        );
      })}
    </div>
  );
};

window.Timeline = Timeline;
window.CallStack = CallStack;
window.fmtMs = fmtMs;
window.fmtTok = fmtTok;
