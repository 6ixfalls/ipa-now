import type { ReactNode } from "react";
import { Sidebar } from "./Sidebar";
export function WorkspaceLayout({
  children,
  activeCount,
  completedCount,
  onQueue,
  onArtifacts,
}: {
  children: ReactNode;
  activeCount: number;
  completedCount: number;
  onQueue: () => void;
  onArtifacts: () => void;
}) {
  return (
    <div className="shell">
      <Sidebar
        activeCount={activeCount}
        completedCount={completedCount}
        onQueue={onQueue}
        onArtifacts={onArtifacts}
      />
      <main>
        <header className="topbar">
          <div>
            <span className="breadcrumb">Workspace</span>
            <span className="slash">/</span>Decryption queue
          </div>
          <span className="local-badge">
            <i /> Self-hosted
          </span>
        </header>
        <div className="content">
          <div className="page-title">
            <div>
              <div className="eyebrow">YOUR APPS, ON YOUR TERMS</div>
              <h1>
                Decryption workspace<span>.</span>
              </h1>
              <p>Request an app. Follow the progress. Keep the IPA.</p>
            </div>
            <div className="version">
              ipa-now <span>v0.1</span>
            </div>
          </div>
          {children}
          <footer>
            <span>
              <i className="footer-dot" /> Stored locally. Served privately.
            </span>
            <span>For legally obtained apps · Trusted private networks</span>
          </footer>
        </div>
      </main>
    </div>
  );
}
