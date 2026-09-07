export function Sidebar({
  activeCount,
  completedCount,
  onQueue,
  onArtifacts,
}: {
  activeCount: number;
  completedCount: number;
  onQueue: () => void;
  onArtifacts: () => void;
}) {
  return (
    <aside className="sidebar">
      <a className="brand" href="/" aria-label="ipa-now home">
        <span className="brand-icon">i.</span>ipa-now
        <span className="brand-dot">●</span>
      </a>
      <div className="workspace-label">YOUR WORKSPACE</div>
      <button type="button" className="nav-item selected" onClick={onQueue}>
        <span>▦</span> Decryption queue{" "}
        <span className="nav-count">{activeCount}</span>
      </button>
      <button type="button" className="nav-item" onClick={onArtifacts}>
        <span>↓</span> Artifacts{" "}
        <span className="nav-count">{completedCount}</span>
      </button>
      <div className="sidebar-bottom">
        <div className="private-icon">⌂</div>
        <strong>Yours, from end to end.</strong>
        <p>
          One device. A private queue.
          <br />
          Your apps stay with you.
        </p>
        <span className="private-tag">PRIVATE NETWORK ONLY</span>
      </div>
    </aside>
  );
}
