// Details panel — Request/Response/Cache/Tokens/Tools/Variables/Diff

const { useState: useState2, useEffect: useEffect2, useRef: useRef2 } = React;

// Syntax-highlight a minimal subset of HTTP+JSON for the raw view
const highlightRaw = (s) => {
  // headers first
  const lines = s.split('\n');
  return lines.map((line, i) => {
    let parts;
    if (i < 6 && /^[A-Z][a-z-]+:/.test(line)) {
      const [k, ...rest] = line.split(':');
      parts = (
        <>
          <span className="h">{k}</span>:{rest.join(':')}
        </>
      );
    } else if (/^(POST|HTTP|GET)/.test(line)) {
      parts = <span className="h">{line}</span>;
    } else {
      parts = line
        .replace(/(".*?")(\s*:)/g, '##K##$1##/K##$2')
        .replace(/:\s*(".*?")/g, ': ##S##$1##/S##')
        .replace(/:\s*([0-9.]+)/g, ': ##N##$1##/N##');
      // split and render
      const tokens = parts.split(/(##\/?[KSN]##)/g);
      let cls = null;
      parts = tokens.map((tok, j) => {
        if (tok === '##K##') { cls = 'k'; return null; }
        if (tok === '##/K##') { const c = cls; cls = null; return null; }
        if (tok === '##S##') { cls = 's'; return null; }
        if (tok === '##/S##') { cls = null; return null; }
        if (tok === '##N##') { cls = 'n'; return null; }
        if (tok === '##/N##') { cls = null; return null; }
        return cls ? <span key={j} className={cls}>{tok}</span> : tok;
      });
    }
    return <div key={i}>{parts}{'\n'.length ? '' : ''}</div>;
  });
};

// ===== Cache visualization variants =====

const CacheSegmented = ({ segments }) => (
  <pre className="prompt-pre">
    {segments.map((s, i) => (
      <div key={i} className={'pseg ' + s.status}>
        <div className="pslabel">
          <span>{s.label}</span>
          <span className="psbadge">
            {s.status === 'hit' ? `cache · ${s.hits || 1}× hits` : s.status === 'new' ? 'new this turn' : 'evicted'}
          </span>
        </div>
        <div className="pstxt">{s.text}</div>
      </div>
    ))}
  </pre>
);

// Heatmap: tokenize approx by whitespace, color by hit count
const CacheHeatmap = ({ segments }) => {
  const allTokens = [];
  segments.forEach((s) => {
    const toks = s.text.split(/(\s+)/);
    toks.forEach((t) => allTokens.push({ t, hit: s.status === 'hit', hits: s.hits || 0, status: s.status }));
  });
  return (
    <div className="heatmap">
      {allTokens.map((tk, i) => {
        if (/^\s+$/.test(tk.t)) return <span key={i}>{tk.t}</span>;
        let bg = 'transparent', color = 'var(--fg)';
        if (tk.status === 'hit') {
          const alpha = Math.min(0.55, 0.18 + tk.hits * 0.025);
          bg = `rgba(111,194,138,${alpha})`;
        } else if (tk.status === 'new') {
          bg = 'rgba(245,160,86,0.16)';
        } else if (tk.status === 'evicted') {
          bg = 'rgba(239,111,108,0.16)';
        }
        return <span key={i} className="tk" style={{ background: bg, color }}>{tk.t}</span>;
      })}
    </div>
  );
};

// Blame: each line tagged with origin turn
const CacheBlame = ({ segments }) => {
  let lineNo = 1;
  return (
    <div className="blame">
      {segments.map((s, i) => {
        const lines = s.text.split('\n');
        return lines.map((ln, j) => {
          const cur = lineNo++;
          return (
            <div key={i + '-' + j} className={'blame-row ' + s.status}>
              <span className="ln">{cur}</span>
              <span className="src">{s.status === 'hit' ? `${s.label} · ${s.hits}×` : s.label}</span>
              <span className="txt">{ln || ' '}</span>
            </div>
          );
        });
      })}
    </div>
  );
};

const CacheView = ({ turn, style }) => {
  if (!turn || !turn.segments) {
    return <div style={{ color: 'var(--fg-3)', fontSize: 12 }}>
      No cache breakdown for this turn (assistant outputs don't have an input prompt of their own — select the next user turn to see what was sent).
    </div>;
  }
  const segs = turn.segments;
  const hitTok = segs.filter(s => s.status === 'hit').reduce((a, s) => a + Math.round(s.text.length / 4), 0);
  const newTok = segs.filter(s => s.status === 'new').reduce((a, s) => a + Math.round(s.text.length / 4), 0);
  const total = hitTok + newTok || 1;
  const hitPct = (hitTok / total) * 100;

  return (
    <>
      <div className="cache-stats">
        <div className="cstat">
          <div className="l">Cache hits</div>
          <div className="v hit">{hitTok.toLocaleString()}</div>
        </div>
        <div className="cstat">
          <div className="l">New tokens</div>
          <div className="v new">{newTok.toLocaleString()}</div>
        </div>
        <div className="cstat">
          <div className="l">Hit rate</div>
          <div className="v">{hitPct.toFixed(0)}%</div>
        </div>
      </div>
      <div className="cache-bar">
        <div className="hit" style={{ width: `${hitPct}%` }}></div>
        <div className="new" style={{ width: `${100 - hitPct}%` }}></div>
      </div>
      <div className="cache-legend">
        <span className="lh"><i></i>Cache hit (reused from previous turn)</span>
        <span className="ln"><i></i>New this turn</span>
      </div>
      {style === 'heatmap' && <CacheHeatmap segments={segs} />}
      {style === 'blame' && <CacheBlame segments={segs} />}
      {(!style || style === 'segmented') && <CacheSegmented segments={segs} />}
      <div style={{ marginTop: 12, fontSize: 11, color: 'var(--fg-3)', fontFamily: 'var(--font-mono)' }}>
        Cache breakpoint: <span style={{ color: 'var(--accent)' }}>after_turn_2</span> · TTL 5min · Min block 1024 tok
      </div>
    </>
  );
};

// ===== Tokens panel =====
const TokensView = ({ turn }) => {
  if (!turn || !turn.tokens) return <div style={{ color: 'var(--fg-3)' }}>No token data.</div>;
  const t = turn.tokens;
  const totalIn = t.cacheHit + t.cacheMiss;
  const total = totalIn + t.completion;
  const c = window.LLMFOX_DATA.cost(t);
  return (
    <>
      <div className="cache-stats">
        <div className="cstat"><div className="l">Input</div><div className="v">{totalIn.toLocaleString()}</div></div>
        <div className="cstat"><div className="l">Output</div><div className="v">{t.completion.toLocaleString()}</div></div>
        <div className="cstat"><div className="l">Cost</div><div className="v">${c.toFixed(4)}</div></div>
      </div>
      <div className="tk-bar">
        {t.cacheHit > 0 && <div className="b-cache" style={{ width: `${(t.cacheHit / total) * 100}%` }}>{Math.round((t.cacheHit/total)*100)}%</div>}
        {t.cacheMiss > 0 && <div className="b-input" style={{ width: `${(t.cacheMiss / total) * 100}%` }}>{Math.round((t.cacheMiss/total)*100)}%</div>}
        {t.completion > 0 && <div className="b-out" style={{ width: `${(t.completion / total) * 100}%` }}>{Math.round((t.completion/total)*100)}%</div>}
      </div>
      <table className="tk-table">
        <tbody>
          <tr><td>cache_read_input_tokens</td><td>{t.cacheHit.toLocaleString()}</td></tr>
          <tr><td>cache_creation_input_tokens</td><td>{t.cacheMiss.toLocaleString()}</td></tr>
          <tr><td>input_tokens (uncached)</td><td>0</td></tr>
          <tr><td>output_tokens</td><td>{t.completion.toLocaleString()}</td></tr>
          <tr className="total"><td>Total</td><td>{total.toLocaleString()}</td></tr>
        </tbody>
      </table>
      <div style={{ marginTop: 12, fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--fg-3)' }}>
        Pricing: cache read $0.30 / MTok · input $3.00 / MTok · output $15.00 / MTok
      </div>
    </>
  );
};

// ===== Tools panel =====
const ToolsView = ({ turn, selectedToolId, onPick }) => {
  if (!turn || !turn.toolCalls || turn.toolCalls.length === 0) {
    return <div style={{ color: 'var(--fg-3)', fontSize: 12 }}>No tool calls on this turn.</div>;
  }
  return (
    <div className="tools-list">
      {turn.toolCalls.map((tc) => {
        const open = selectedToolId === tc.id || turn.toolCalls.length === 1;
        return (
          <div key={tc.id} className="tools-card">
            <div className="th" onClick={() => onPick && onPick(tc.id)} style={{ cursor: onPick ? 'pointer' : 'default' }}>
              <Icon name="tool" size={12} style={{ color: 'var(--tool)' }} />
              <span className="tname">{tc.name}</span>
              <span style={{ marginLeft: 'auto', display: 'flex', gap: 8, alignItems: 'center' }}>
                <span style={{ color: 'var(--fg-4)', fontFamily: 'var(--font-mono)', fontSize: 11 }}>{fmtMs(tc.duration)}</span>
                <span className={'tstat ' + tc.status} style={{ fontSize: 10, padding: '1px 6px', borderRadius: 3, letterSpacing: '0.04em', textTransform: 'uppercase', color: tc.status === 'ok' ? 'var(--hit)' : 'var(--err)', background: tc.status === 'ok' ? 'var(--hit-soft)' : 'rgba(239,111,108,0.14)' }}>
                  {tc.status}
                </span>
              </span>
            </div>
            <div className="l">arguments</div>
            <pre className="targs">{JSON.stringify(tc.args, null, 2)}</pre>
            {open && (
              <>
                <div className="l" style={{ marginTop: 8 }}>result</div>
                <pre className="tres">{tc.result}</pre>
              </>
            )}
          </div>
        );
      })}
    </div>
  );
};

// ===== Raw view + streaming playback =====
const RawView = ({ kind, session, turn }) => {
  const [progress, setProgress] = useState2(1);
  const [playing, setPlaying] = useState2(false);
  const tref = useRef2(null);

  useEffect2(() => {
    if (!playing) return;
    const start = performance.now() - progress * 2400;
    const tick = () => {
      const p = Math.min(1, (performance.now() - start) / 2400);
      setProgress(p);
      if (p < 1) tref.current = requestAnimationFrame(tick);
      else setPlaying(false);
    };
    tref.current = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(tref.current);
  }, [playing]);

  const isResp = kind === 'response';
  const raw = isResp ? window.LLMFOX_DATA.RAW_RESPONSE_T3 : window.LLMFOX_DATA.RAW_REQUEST_T3;
  // for streaming display, slice the response by progress
  const display = (isResp && turn && turn.streaming)
    ? raw.slice(0, Math.floor(raw.length * progress))
    : raw;

  return (
    <>
      {isResp && turn && turn.streaming && (
        <div className="stream-controls">
          <button className="play" onClick={() => { if (progress >= 1) setProgress(0); setPlaying(!playing); }}>
            <Icon name={playing ? 'pause' : 'play'} size={11} />
          </button>
          <span>{Math.round(progress * 100)}%</span>
          <div className="stream-progress"><i style={{ width: `${progress * 100}%` }} /></div>
          <span style={{ color: 'var(--fg-4)' }}>streamed · 2.4s · {Math.round(progress * 524)} tok</span>
        </div>
      )}
      <pre className="raw">{display}{isResp && progress < 1 && '▍'}</pre>
    </>
  );
};

// ===== Diff view =====
const DiffView = () => {
  return (
    <>
      <div style={{ display: 'flex', gap: 10, marginBottom: 10, fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--fg-3)' }}>
        <span>turn t3 · original</span>
        <span style={{ color: 'var(--fg-4)' }}>vs</span>
        <span>turn t3 · replay (temperature=0.7)</span>
      </div>
      <div className="diff">
        {window.LLMFOX_DATA.DIFF_T3.map((d, i) => (
          <div key={i} className={'diff-row ' + d.kind}>
            <span className="sgn">{d.kind === 'add' ? '+' : d.kind === 'del' ? '−' : ' '}</span>
            <span className="txt">{d.text || ' '}</span>
          </div>
        ))}
      </div>
    </>
  );
};

// ===== The shell =====

const DetailsPanel = ({ session, selection, cacheStyle, onSelect }) => {
  const [tab, setTab] = useState2('cache');
  const turn = session.turns.find(t => t.id === selection.turnId) || session.turns[0];
  const tool = turn.toolCalls?.find(tc => tc.id === selection.toolId);

  // When a tool is selected, default to Tools tab
  useEffect2(() => {
    if (selection.toolId) setTab('tools');
  }, [selection.toolId]);

  const tabs = [
    { id: 'cache',   label: 'Cache',     ico: 'flame' },
    { id: 'tokens',  label: 'Tokens',    ico: 'spark' },
    { id: 'tools',   label: 'Tools',     ico: 'tool' },
    { id: 'request', label: 'Request',   ico: 'code' },
    { id: 'response',label: 'Response',  ico: 'code' },
    { id: 'diff',    label: 'Diff',      ico: 'diff' },
  ];

  const title = tool
    ? <>tool · <span style={{ fontFamily: 'var(--font-mono)' }}>{tool.name}</span></>
    : <>turn · <span style={{ fontFamily: 'var(--font-mono)' }}>{turn.id}</span> · <span style={{ color: 'var(--fg-3)', fontWeight: 400, textTransform: 'capitalize' }}>{turn.kind}</span></>;

  return (
    <aside className="details">
      <div className="det-head">
        <div className="deth1">
          {title}
          <span style={{ marginLeft: 'auto', display: 'flex', gap: 6 }}>
            <button className="tb-btn" title="Open as new tab"><Icon name="plus" size={11} /></button>
          </span>
        </div>
        <div className="deth2">
          <span>session {session.id}</span>
          <span>·</span>
          <span>{turn.time}</span>
          {turn.duration != null && <><span>·</span><span>{fmtMs(turn.duration)}</span></>}
          {turn.tokens && <><span>·</span><span>{fmtTok(turn.tokens.prompt + turn.tokens.completion)} tok</span></>}
          {turn.tokens && <><span>·</span><span style={{ color: 'var(--hit)' }}>
            {Math.round((turn.tokens.cacheHit / Math.max(1, turn.tokens.cacheHit + turn.tokens.cacheMiss)) * 100)}% cached
          </span></>}
        </div>
      </div>
      <div className="det-tabs">
        {tabs.map((t) => (
          <div
            key={t.id}
            className={'det-tab' + (tab === t.id ? ' active' : '')}
            onClick={() => setTab(t.id)}
          >
            {t.label}
          </div>
        ))}
      </div>
      <div className="det-body">
        {tab === 'cache' && <CacheView turn={turn} style={cacheStyle} />}
        {tab === 'tokens' && <TokensView turn={turn} />}
        {tab === 'tools' && <ToolsView turn={turn} selectedToolId={selection.toolId} onPick={(id) => onSelect(turn.id, id)} />}
        {tab === 'request' && <RawView kind="request" session={session} turn={turn} />}
        {tab === 'response' && <RawView kind="response" session={session} turn={turn} />}
        {tab === 'diff' && <DiffView />}
      </div>
    </aside>
  );
};

window.DetailsPanel = DetailsPanel;
