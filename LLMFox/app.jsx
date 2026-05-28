// LLMFox — main app shell

const { useState: useS, useEffect: useE, useRef: useR } = React;

const DEFAULTS = /*EDITMODE-BEGIN*/{
  "view": "timeline",
  "cacheStyle": "segmented",
  "theme": "dark",
  "sidebarWidth": 264,
  "detailsWidth": 460,
  "debugHeight": 200
}/*EDITMODE-END*/;

const App = () => {
  const SESSIONS = window.LLMFOX_DATA.SESSIONS;
  const [t, setTweak] = useTweaks(DEFAULTS);

  // Selection state
  const [activeSession, setActiveSession] = useS('s-7f2a');
  const [selection, setSelection] = useS({ turnId: 't3', toolId: null });
  const [openTabs, setOpenTabs] = useS(['s-7f2a']);
  const [breakpoints, setBreakpoints] = useS(['t3']);
  const [streamingTurnId, setStreamingTurnId] = useS(null);
  const [streamingProgress, setStreamingProgress] = useS(0);
  const [paused, setPaused] = useS(true);
  const [replayOpen, setReplayOpen] = useS(false);
  const [lastReplay, setLastReplay] = useS(null);
  const [replayCfg, setReplayCfg] = useS({ temperature: 0.7, top_p: 0.95, model: 'claude-sonnet-4-5' });

  const session = SESSIONS.find(s => s.id === activeSession) || SESSIONS[0];

  // theme
  useE(() => { document.documentElement.dataset.theme = t.theme; }, [t.theme]);

  // Selection helpers
  const pickSession = (id) => {
    setActiveSession(id);
    if (!openTabs.includes(id)) setOpenTabs([...openTabs, id]);
    const s = SESSIONS.find(x => x.id === id);
    setSelection({ turnId: s.turns[0].id, toolId: null });
    setStreamingTurnId(null);
  };
  const closeTab = (id) => {
    const next = openTabs.filter(x => x !== id);
    setOpenTabs(next);
    if (id === activeSession && next.length > 0) setActiveSession(next[next.length - 1]);
  };
  const onSelect = (turnId, toolId) => setSelection({ turnId, toolId });
  const toggleBp = (id) => {
    setBreakpoints(breakpoints.includes(id) ? breakpoints.filter(x => x !== id) : [...breakpoints, id]);
  };

  // Streaming sim
  const streamRef = useR(null);
  const playStreaming = (turnId) => {
    const turn = session.turns.find(x => x.id === turnId);
    if (!turn || !turn.streaming) return;
    setStreamingTurnId(turnId);
    setStreamingProgress(0);
    const start = performance.now();
    const dur = 2400;
    const tick = () => {
      const p = Math.min(1, (performance.now() - start) / dur);
      setStreamingProgress(p);
      if (p < 1) streamRef.current = requestAnimationFrame(tick);
      else { streamRef.current = null; setTimeout(() => setStreamingTurnId(null), 600); }
    };
    streamRef.current = requestAnimationFrame(tick);
  };
  useE(() => () => streamRef.current && cancelAnimationFrame(streamRef.current), []);

  const runDebug = () => {
    setPaused(false);
    // Find first breakpoint -> select it & pause
    const bp = session.turns.find(x => breakpoints.includes(x.id));
    if (bp) {
      setSelection({ turnId: bp.id, toolId: null });
      setTimeout(() => setPaused(true), 320);
    }
  };

  const submitReplay = () => {
    setLastReplay({ turnId: selection.turnId, ...replayCfg });
    setReplayOpen(false);
  };

  return (
    <div className="app" style={{
      '--col-left': t.sidebarWidth + 'px',
      '--col-right': t.detailsWidth + 'px',
      '--debug-h': t.debugHeight + 'px',
    }}>
      {/* ====== TOP BAR ====== */}
      <div className="topbar">
        <div className="brand">
          <span className="brand-mark"></span>
          <span>LLMFox</span>
        </div>
        <span className="topbar-sep"></span>
        <span className="crumb">
          <span>{session.project}</span>
          <span style={{ color: 'var(--fg-4)', padding: '0 6px' }}>/</span>
          <b>{session.title}</b>
        </span>
        <span className="env-chip">
          <span className="dot"></span>
          <span>prod · anthropic</span>
          <Icon name="chevdown" size={10} />
        </span>

        <div className="topbar-spacer"></div>

        <div className="topbar-actions">
          <button className="tb-btn" title="Step over" onClick={() => onSelect(session.turns[Math.min(session.turns.findIndex(x => x.id === selection.turnId) + 1, session.turns.length - 1)].id, null)}>
            <Icon name="step-over" size={13} /> Step
          </button>
          <button className="tb-btn" title="Restart session" onClick={() => { setSelection({ turnId: session.turns[0].id, toolId: null }); setStreamingTurnId(null); }}>
            <Icon name="restart" size={13} />
          </button>
          <button className="tb-btn primary" onClick={runDebug}>
            <Icon name={paused ? 'play' : 'pause'} size={11} />
            <span>{paused ? 'Continue' : 'Running'}</span>
            <span className="kbd">F5</span>
          </button>
          <span className="topbar-sep"></span>
          <button className="tb-btn" onClick={() => setReplayOpen(!replayOpen)}>
            <Icon name="branch" size={13} /> Replay…
          </button>
          <button className="tb-btn" onClick={() => playStreaming(selection.turnId)} title="Play streaming for selected turn">
            <Icon name="play" size={11} /> Stream
          </button>
          <span className="topbar-sep"></span>
          <button className="tb-btn" onClick={() => setTweak('theme', t.theme === 'dark' ? 'light' : 'dark')} title="Toggle theme">
            <Icon name={t.theme === 'dark' ? 'sun' : 'moon'} size={13} />
          </button>
          <button className="tb-btn" title="Settings"><Icon name="settings" size={13} /></button>
        </div>
      </div>

      {/* ====== MAIN ====== */}
      <div className="main">
        <Sidebar sessions={SESSIONS} activeId={activeSession} onPick={pickSession} />

        <div className="center">
          <div className="center-tabs">
            {openTabs.map((id) => {
              const s = SESSIONS.find(x => x.id === id);
              if (!s) return null;
              return (
                <div
                  key={id}
                  className={'ctab' + (activeSession === id ? ' active' : '')}
                  onClick={() => setActiveSession(id)}
                >
                  <Icon name={s.status === 'failed' ? 'bug' : 'bot'} size={11} className="ico"
                    style={{ color: s.status === 'failed' ? 'var(--err)' : 'var(--accent)' }} />
                  <span>{s.title}</span>
                  <span className="x" onClick={(e) => { e.stopPropagation(); closeTab(id); }}>
                    <Icon name="x" size={9} />
                  </span>
                </div>
              );
            })}
            <div className="center-tabs-spacer"></div>
            <div className="center-tools">
              <div className="seg">
                <button className={t.view === 'timeline' ? 'active' : ''} onClick={() => setTweak('view', 'timeline')}>
                  <Icon name="list" size={10} /> Timeline
                </button>
                <button className={t.view === 'stack' ? 'active' : ''} onClick={() => setTweak('view', 'stack')}>
                  <Icon name="stack" size={10} /> Call stack
                </button>
              </div>
              <button className="tb-btn"><Icon name="filter" size={11} /> Filter</button>
            </div>
          </div>

          <div className="canvas" id="canvas">
            {t.view === 'timeline'
              ? <Timeline session={session} selection={selection} onSelect={onSelect}
                  streamingTurnId={streamingTurnId} streamingProgress={streamingProgress}
                  onToggleBp={toggleBp} />
              : <CallStack session={session} selection={selection} onSelect={onSelect} />
            }
            {replayOpen && (
              <div className="replay-pop" onClick={(e) => e.stopPropagation()}>
                <h4>Replay turn <span style={{ fontFamily: 'var(--font-mono)', color: 'var(--accent)' }}>{selection.turnId}</span></h4>
                <div style={{ fontSize: 11, color: 'var(--fg-3)', marginBottom: 8 }}>Re-runs the assistant turn with overridden parameters. Result appears in the Diff tab.</div>
                <div className="replay-row">
                  <label>model</label>
                  <select value={replayCfg.model} onChange={(e) => setReplayCfg({ ...replayCfg, model: e.target.value })}>
                    <option>claude-sonnet-4-5</option>
                    <option>claude-opus-4-5</option>
                    <option>claude-haiku-4-5</option>
                  </select>
                </div>
                <div className="replay-row">
                  <label>temperature</label>
                  <input type="number" step="0.1" min="0" max="1" value={replayCfg.temperature}
                    onChange={(e) => setReplayCfg({ ...replayCfg, temperature: parseFloat(e.target.value) })} />
                </div>
                <div className="replay-row">
                  <label>top_p</label>
                  <input type="number" step="0.05" min="0" max="1" value={replayCfg.top_p}
                    onChange={(e) => setReplayCfg({ ...replayCfg, top_p: parseFloat(e.target.value) })} />
                </div>
                <div className="replay-row">
                  <label>cache</label>
                  <select defaultValue="reuse">
                    <option value="reuse">Reuse existing cache</option>
                    <option value="fresh">Bypass cache</option>
                    <option value="warm">Warm new breakpoint</option>
                  </select>
                </div>
                <div className="actions">
                  <button className="tb-btn" onClick={() => setReplayOpen(false)}>Cancel</button>
                  <button className="tb-btn primary" onClick={submitReplay}><Icon name="play" size={10} /> Run replay</button>
                </div>
              </div>
            )}
          </div>

          <Debug session={session} streamingTurnId={streamingTurnId}
            breakpoints={breakpoints} onToggleBp={toggleBp} lastReplay={lastReplay} />
        </div>

        <DetailsPanel session={session} selection={selection} cacheStyle={t.cacheStyle} onSelect={onSelect} />
      </div>

      {/* ====== STATUS BAR ====== */}
      <div className="statusbar">
        <span>{paused ? <span className="acc">⏸ paused</span> : <span className="ok">▸ running</span>}</span>
        <span>·</span>
        <span>session {session.id}</span>
        <span>·</span>
        <span>{session.turns.length} turns</span>
        <span>·</span>
        <span>{fmtTok(session.totalTokens)} tok ({Math.round(session.cachedTokens / session.totalTokens * 100)}% cached)</span>
        <span>·</span>
        <span>${session.cost.toFixed(4)}</span>
        <span>·</span>
        <span>{breakpoints.length} breakpoint{breakpoints.length !== 1 ? 's' : ''}</span>
        <div className="seg-spacer"></div>
        <span>{session.model}</span>
        <span>·</span>
        <span>cache: ephemeral · 5m</span>
        <span>·</span>
        <span className="acc">{t.cacheStyle}</span>
        <span>·</span>
        <span>{t.theme} theme</span>
      </div>

      {/* ====== TWEAKS PANEL ====== */}
      <TweaksPanel>
        <TweakSection label="View">
          <TweakRadio
            label="Center pane"
            value={t.view}
            options={[{ value: 'timeline', label: 'Timeline' }, { value: 'stack', label: 'Call stack' }]}
            onChange={(v) => setTweak('view', v)}
          />
          <TweakRadio
            label="Theme"
            value={t.theme}
            options={[{ value: 'dark', label: 'Dark' }, { value: 'light', label: 'Light' }]}
            onChange={(v) => setTweak('theme', v)}
          />
        </TweakSection>
        <TweakSection label="Cache visualization">
          <TweakRadio
            label="Style"
            value={t.cacheStyle}
            options={[
              { value: 'segmented', label: 'Segmented' },
              { value: 'heatmap', label: 'Heatmap' },
              { value: 'blame', label: 'Blame' },
            ]}
            onChange={(v) => setTweak('cacheStyle', v)}
          />
          <div style={{ fontSize: 10.5, color: 'rgba(41,38,27,.6)', lineHeight: 1.5 }}>
            <b>Segmented</b>: cache regions w/ hit counts. <b>Heatmap</b>: per-token reuse tint. <b>Blame</b>: per-line origin turn.
          </div>
        </TweakSection>
        <TweakSection label="Layout">
          <TweakSlider label="Sidebar width" value={t.sidebarWidth} min={200} max={360} step={4} unit="px"
            onChange={(v) => setTweak('sidebarWidth', v)} />
          <TweakSlider label="Details width" value={t.detailsWidth} min={360} max={620} step={4} unit="px"
            onChange={(v) => setTweak('detailsWidth', v)} />
          <TweakSlider label="Debug pane height" value={t.debugHeight} min={120} max={360} step={4} unit="px"
            onChange={(v) => setTweak('debugHeight', v)} />
        </TweakSection>
      </TweaksPanel>
    </div>
  );
};

ReactDOM.createRoot(document.getElementById('root')).render(<App />);
