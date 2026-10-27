import { Routes, Route, Navigate, useMatch } from 'react-router-dom';
import { Nav } from '@/components/Nav';
import { DashboardPage } from '@/pages/Dashboard';
import { MatchesPage } from '@/pages/Matches';
import { MatchDetailPage } from '@/pages/MatchDetail';
import { EvaluationPage } from '@/pages/Evaluation';

export function App() {
  const onMatchDetail = useMatch('/matches/:id') !== null;
  return (
    <div className={`app-shell${onMatchDetail ? ' app-shell--wide' : ''}`}>
      <Nav />
      <main className="app-main" role="main">
        <Routes>
          <Route path="/" element={<DashboardPage />} />
          <Route path="/matches" element={<MatchesPage />} />
          <Route path="/matches/:id" element={<MatchDetailPage />} />
          <Route path="/evaluation" element={<EvaluationPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
    </div>
  );
}
