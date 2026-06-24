// Bottom Debug console panel (variables, console, breakpoints, problems)

const { useState: useStateD, useEffect: useEffectD } = React;

const ConsoleLog = ({ session, streamingTurnId, breakpoints, lastReplay }) => {
  const lines = [];
  lines.push({ t: '09:14:22', lvl: 'info', text: `session ${session.id} started · model=${session.model}` });
  lines.push({ t: '09:14:22', lvl: 'info', text: `cache breakpoints: after_system, after_turn_2 · ttl=300s` });
  session.turns.forEach((tu, i) => {
    if (tu.kind === 'user') {
      lines.push({ t: tu.time, lvl: 'info', text: `→ user.message len=${tu.content.length} chars` });
    } else {
      if (tu.tokens) {
        const hit = tu.tokens.cacheHit;
        const miss = tu.tokens.cacheMiss;
        lines.push({ t: tu.time, lvl: 'info', text: `← assistant.respond input=${hit + miss} (cache ${hit}, new ${miss}) output=${tu.tokens.completion} in ${fmtMs(tu.duration)}` });
      }
      (tu.toolCalls || []).forEach((tc) => {
        const lvl = tc.status === 'ok' ? 'info' : tc.status === 'fail' ? 'warn' : 'err';
        lines.push({ t: tu.time, lvl, text: `  ⤷ tool ${tc.name}(${Object.keys(tc.args).join(',')}) → ${tc.status} in ${fmtMs(tc.duration)}` });
      });
      if (tu.error) lines.push({ t: tu.time, lvl: 'err', text: `! ${tu.error}` });
    }
  });
  if (breakpoints.length > 0) {
    lines.push({ t: '09:15:33', lvl: 'warn', text: `paused at breakpoint(s): ${breakpoints.join(', ')}` });
  }
  if (lastReplay) {
    lines.push({ t: '09:16:02', lvl: 'info', text: `replay ${lastReplay.turnId} with temperature=${lastReplay.temperature} top_p=${lastReplay.top_p} — see Diff tab` });
  }
  if (streamingTurnId) {
    lines.push({ t: '—', lvl: 'info', text: `streaming ${streamingTurnId}…` });
  }

  return (
    <div>
      {lines.map((l, i) => (
        <div key={i} className="console-line">
          <span className="ctime">{l.t}</span>
          <span className={'clvl ' + l.lvl}>{l.lvl === 'info' ? 'ℹ' : l.lvl === 'warn' ? '⚠' : '✕'}</span>
          <span className="ctext">{l.text}</span>
        </div>
      ))}
    </div>
  );
};

const Variables = () => {
  const vars = window.LLMFOX_DATA.VARIABLES_BY_TURN['t3'] || [];
  const [expanded, setExpanded] = useStateD({});
  return (
    <div>
      <div className="var-row" style={{ color: 'var(--fg-3)', fontSize: 10, letterSpacing: '0.06em', textTransform: 'uppercase' }}>
        <span></span>
        <span>name</span>
        <span>type</span>
        <span>value</span>
      </div>
      {vars.map((v, i) => (
        <React.Fragment key={i}>
          <div className="var-row">
            <span className="chev" onClick={() => setExpanded({ ...expanded, [v.name]: !expanded[v.name] })}>
              {v.expandable ? (expanded[v.name] ? '▾' : '▸') : ' '}
            </span>
            <span className="vname">{v.name}</span>
            <span className="vtype">{v.type}</span>
            <span className="vval" title={v.value}>{v.value}</span>
          </div>
          {expanded[v.name] && v.name === 'tools' && (
            <div style={{ paddingLeft: 24, color: 'var(--fg-3)' }}>
              {['read_file', 'write_file', 'run_tests', 'search_code', 'run_shell'].map((tn, k) => (
                <div key={k} className="var-row">
                  <span></span>
                  <span className="vname">[{k}]</span>
                  <span className="vtype">function</span>
                  <span className="vval">{tn}(…)</span>
                </div>
              ))}
            </div>
          )}
        </React.Fragment>
      ))}
    </div>
  );
};

const Breakpoints = ({ session, breakpoints, onToggle }) => {
  return (
    <div>
      {breakpoints.length === 0 && (
        <div style={{ color: 'var(--fg-3)' }}>No breakpoints. Click the gutter next to any turn in the timeline to set one.</div>
      )}
      {breakpoints.map((id) => {
        const t = session.turns.find(x => x.id === id);
        return (
          <div key={id} className="console-line">
            <span className="ctime">{t?.time || '—'}</span>
            <span style={{ color: 'var(--bp)' }}>●</span>
            <span className="ctext">
              <span style={{ color: 'var(--fg) ' }}>{session.id} · {id}</span>
              <span style={{ color: 'var(--fg-3)', marginLeft: 8 }}>{t?.kind} · pause before send</span>
              <button className="tb-btn" style={{ marginLeft: 12 }} onClick={() => onToggle(id)}>remove</button>
            </span>
          </div>
        );
      })}
    </div>
  );
};

const Problems = ({ session }) => {
  const probs = [];
  session.turns.forEach((t) => {
    (t.toolCalls || []).forEach((tc) => {
      if (tc.status === 'fail' || tc.status === 'error') {
        probs.push({ t: t.time, where: `${t.id} / ${tc.name}`, msg: tc.result.split('\n').slice(-2).join(' · ') });
      }
    });
    if (t.error) probs.push({ t: t.time, where: t.id, msg: t.error });
  });
  if (probs.length === 0) return <div style={{ color: 'var(--fg-3)' }}>No problems detected in this session.</div>;
  return (
    <div>
      {probs.map((p, i) => (
        <div key={i} className="console-line">
          <span className="ctime">{p.t}</span>
          <span className="clvl err">✕</span>
          <span className="ctext">
            <span style={{ color: 'var(--err)' }}>{p.where}</span> — {p.msg}
          </span>
        </div>
      ))}
    </div>
  );
};

const Debug = ({ session, streamingTurnId, breakpoints, onToggleBp, lastReplay }) => {
  const [tab, setTab] = useStateD('console');
  return (
    <div className="debug">
      <div className="debug-tabs">
        <div className={'dtab' + (tab === 'console' ? ' active' : '')} onClick={() => setTab('console')}>Debug Console</div>
        <div className={'dtab' + (tab === 'vars' ? ' active' : '')} onClick={() => setTab('vars')}>Variables</div>
        <div className={'dtab' + (tab === 'bps' ? ' active' : '')} onClick={() => setTab('bps')}>
          Breakpoints {breakpoints.length > 0 && <span style={{ color: 'var(--bp)', marginLeft: 4 }}>({breakpoints.length})</span>}
        </div>
        <div className={'dtab' + (tab === 'problems' ? ' active' : '')} onClick={() => setTab('problems')}>Problems</div>
        <div className="debug-tabs-spacer"></div>
        <button className="tb-btn" title="Clear"><Icon name="x" size={11} /></button>
      </div>
      <div className="debug-body">
        {tab === 'console' && <ConsoleLog session={session} streamingTurnId={streamingTurnId} breakpoints={breakpoints} lastReplay={lastReplay} />}
        {tab === 'vars' && <Variables />}
        {tab === 'bps' && <Breakpoints session={session} breakpoints={breakpoints} onToggle={onToggleBp} />}
        {tab === 'problems' && <Problems session={session} />}
      </div>
    </div>
  );
};

window.Debug = Debug;
