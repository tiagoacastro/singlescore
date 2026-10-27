import { NavLink } from 'react-router-dom';
import { useQuery } from '@apollo/client';
import { gql } from '@apollo/client';
import type { Health } from '@/graphql/types';
import { ProvenanceChip } from './Chip';

const SYSTEM_STATUS_QUERY = gql`
  query SystemStatus {
    health {
      status
      databaseReachable
      demoMode
      version
    }
  }
`;

export function Nav() {
  const { data } = useQuery<{ health: Health }>(SYSTEM_STATUS_QUERY, {
    pollInterval: 15000,
    fetchPolicy: 'cache-and-network',
    errorPolicy: 'all',
  });
  const health = data?.health;

  return (
    <header className="nav">
      <div className="nav__inner">
        <NavLink to="/" className="nav__brand" aria-label="SingleScore home">
          SingleScore<span className="accent">.</span>
        </NavLink>
        <nav className="nav__links" aria-label="Primary">
          <NavLink
            to="/"
            end
            className={({ isActive }) => `nav__link${isActive ? ' is-active' : ''}`}
          >
            Dashboard
          </NavLink>
          <NavLink
            to="/matches"
            className={({ isActive }) => `nav__link${isActive ? ' is-active' : ''}`}
          >
            Matches
          </NavLink>
          <NavLink
            to="/evaluation"
            className={({ isActive }) => `nav__link${isActive ? ' is-active' : ''}`}
          >
            Model evaluation
          </NavLink>
        </nav>
        <div className="nav__spacer" />
        <div className="nav__status" role="status" aria-live="polite">
          {health?.demoMode ? <ProvenanceChip provenance="DEMO" /> : null}
          <span>
            db:{' '}
            <strong style={{ color: health?.databaseReachable ? '#38d996' : '#ff5a68' }}>
              {health ? (health.databaseReachable ? 'ok' : 'unreachable') : '…'}
            </strong>
          </span>
          <span>v{health?.version ?? 'dev'}</span>
        </div>
      </div>
    </header>
  );
}
