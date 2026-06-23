import { PermanentAgentPage } from './pages/PermanentAgentPage';
import { WorkflowTaskPage } from './pages/WorkflowTaskPage';

export default function App() {
  if (window.location.pathname === '/agent') {
    return <PermanentAgentPage />;
  }

  return <WorkflowTaskPage />;
}
