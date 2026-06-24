// Sidebar - sessions tree

const { useState } = React;

const Sidebar = ({ sessions, activeId, onPick }) => {
  const [filter, setFilter] = useState('');
  const [openProj, setOpenProj] = useState({ 'checkout-service': true, 'support-bot': true });

  const byProject = sessions.reduce((acc, s) => {
    (acc[s.project] = acc[s.project] || []).push(s);
    return acc;
  }, {});

  const matches = (s) =>
    !filter ||
    s.title.toLowerCase().includes(filter.toLowerCase()) ||
    s.id.includes(filter);

  return (
    <aside className="sidebar">
      <div className="side-head">
        <div className="label">Sessions</div>
        <button className="tb-btn" title="New session"><Icon name="plus" size={12} /></button>
      </div>
      <div className="side-filter">
        <Icon name="search" size={12} style={{ color: 'var(--fg-4)' }} />
        <input
          placeholder="Filter sessions, ids, models…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        {filter && <button className="tb-btn" onClick={() => setFilter('')} style={{ padding: '0 4px' }}><Icon name="x" size={10} /></button>}
      </div>
      <div className="side-list">
        {Object.entries(byProject).map(([proj, list]) => {
          const open = openProj[proj];
          const visible = list.filter(matches);
          if (visible.length === 0) return null;
          return (
            <div key={proj} className={'tree-group' + (open ? '' : ' collapsed')}>
              <div className="tree-group-hdr" onClick={() => setOpenProj({ ...openProj, [proj]: !open })}>
                <Icon name="chevdown" size={10} className="chev" />
                <Icon name="db" size={11} />
                <span style={{ marginLeft: 4 }}>{proj}</span>
                <span style={{ marginLeft: 'auto', color: 'var(--fg-4)', fontFamily: 'var(--font-mono)' }}>{list.length}</span>
              </div>
              {open && visible.map((s) => (
                <React.Fragment key={s.id}>
                  <div
                    className={
                      'session-item' +
                      (s.id === activeId ? ' active' : '') +
                      (s.status === 'failed' ? ' failed' : '')
                    }
                    onClick={() => onPick(s.id)}
                  >
                    <span className="sdot"></span>
                    <span className="stitle">{s.title}</span>
                    <span className="smeta">{s.id.slice(2)}</span>
                  </div>
                  <div className="session-sub">
                    <span>{s.turns.length} turns</span>
                    <span>·</span>
                    <span>{(s.totalTokens / 1000).toFixed(1)}k tok</span>
                    <span>·</span>
                    <span>${s.cost.toFixed(3)}</span>
                  </div>
                </React.Fragment>
              ))}
            </div>
          );
        })}
      </div>
      <div style={{ padding: 10, borderTop: '1px solid var(--border)', fontSize: 11, color: 'var(--fg-3)', fontFamily: 'var(--font-mono)' }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 4 }}>
          <span>Today</span><span>{sessions.length} sessions</span>
        </div>
        <div style={{ display: 'flex', justifyContent: 'space-between' }}>
          <span style={{ color: 'var(--fg-4)' }}>cache hit</span>
          <span style={{ color: 'var(--hit)' }}>
            {Math.round(
              sessions.reduce((s, x) => s + x.cachedTokens, 0) /
              sessions.reduce((s, x) => s + x.totalTokens, 0) * 100
            )}%
          </span>
        </div>
      </div>
    </aside>
  );
};

window.Sidebar = Sidebar;
