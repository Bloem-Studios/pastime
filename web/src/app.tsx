import React, { Suspense } from 'react';
import { createRoot } from 'react-dom/client';
import { createBrowserRouter, Navigate, Outlet, RouterProvider } from 'react-router-dom';
import { QueryClientProvider } from '@tanstack/react-query';
import '@fontsource-variable/outfit';
import './styles.css';
import './pages/account.css';
import { AuthProvider, DirtyProvider, queryClient } from './state';
import { AdminGuard, ProtectedLayout } from './layout';
import { ConfirmProvider, Loading, NotFound } from './ui';
import { t } from './i18n';

function Root() { return <AuthProvider><ConfirmProvider><DirtyProvider><Outlet /></DirtyProvider></ConfirmProvider></AuthProvider>; }
function RouteFailure() { return <main id="main" className="auth-loading"><h1>{t('This page could not be loaded')}</h1><p>{t('No automatic write retry was performed.')}</p><button className="button" onClick={() => window.location.reload()}>{t('Reload page')}</button></main>; }
const router = createBrowserRouter([{ element: <Root />, errorElement: <RouteFailure />, children: [
  { path: '/login', lazy: async () => ({ Component: (await import('./pages/auth')).Login }) },
  { path: '/join', lazy: async () => ({ Component: (await import('./pages/auth')).Join }) },
  { path: '/register', lazy: async () => ({ Component: (await import('./pages/auth')).Register }) },
  { path: '/recover', lazy: async () => ({ Component: (await import('./pages/auth')).Recover }) },
  { path: '/u/:username', lazy: async () => ({ Component: (await import('./pages/public')).PublicProfilePage }) },
  { path: '/u/:username/:slug', lazy: async () => ({ Component: (await import('./pages/public')).PublicListPage }) },
  { element: <ProtectedLayout />, children: [
    { index: true, lazy: async () => ({ Component: (await import('./pages/home')).Home }) },
    { path: '/library', lazy: async () => ({ Component: (await import('./pages/library')).Library }) },
    { path: '/library/:entryID', lazy: async () => ({ Component: (await import('./pages/item')).MediaDetail }) },
    { path: '/discover', lazy: async () => ({ Component: (await import('./pages/discover')).Discover }) },
    { path: '/title/:catalogID', lazy: async () => ({ Component: (await import('./pages/title')).TitleDetail }) },
    { path: '/people/:provider/:personID', lazy: async () => ({ Component: (await import('./pages/people')).PersonPage }) },
    { path: '/studios/:provider/:studioKind/:studioID', lazy: async () => ({ Component: (await import('./pages/people')).StudioPage }) },
    { path: '/recommendations', lazy: async () => ({ Component: (await import('./pages/people')).RecommendationsPage }) },
    { path: '/history', lazy: async () => ({ Component: (await import('./pages/history')).HistoryPage }) },
    { path: '/calendar', lazy: async () => ({ Component: (await import('./pages/calendar')).CalendarPage }) },
    { path: '/stats', lazy: async () => ({ Component: (await import('./pages/stats')).StatsPage }) },
    { path: '/review', lazy: async () => ({ Component: (await import('./pages/review')).ReviewPage }) },
    { path: '/review/:year', lazy: async () => ({ Component: (await import('./pages/review')).ReviewPage }) },
    { path: '/integrations/unmatched', lazy: async () => ({ Component: (await import('./pages/transfers')).UnmatchedPage }) },
    { path: '/integrations', lazy: async () => ({ Component: (await import('./pages/integrations')).IntegrationsPage }) },
    { path: '/more', lazy: async () => ({ Component: (await import('./pages/more')).MorePage }) },
    { path: '/activity', lazy: async () => ({ Component: (await import('./pages/activity')).ActivityPage }) },
    { path: '/collection', lazy: async () => ({ Component: (await import('./pages/collection')).CollectionPage }) },
    // In Silo browsing retired with the Silo library bridge; old links land on Home.
    { path: '/library/available', element: <Navigate to="/" replace /> },
    { path: '/lists', lazy: async () => ({ Component: (await import('./pages/lists')).ListsPage }) },
    { path: '/collab/:collabID', lazy: async () => ({ Component: (await import('./pages/collab')).CollabDetailPage }) },
    { path: '/lists/:listID', lazy: async () => ({ Component: (await import('./pages/lists')).ListDetail }) },
    { path: '/account', lazy: async () => ({ Component: (await import('./pages/account')).AccountPage }) },
    { path: '/device', lazy: async () => ({ Component: (await import('./pages/device')).DevicePage }) },
    { element: <AdminGuard />, children: [
      { path: '/admin', lazy: async () => ({ Component: (await import('./pages/console')).Dashboard }) },
      { path: '/admin/jobs', lazy: async () => ({ Component: (await import('./pages/console')).JobsPage }) },
      { path: '/admin/providers', lazy: async () => ({ Component: (await import('./pages/console')).ProvidersPage }) },
      { path: '/admin/catalog', lazy: async () => ({ Component: (await import('./pages/console')).CatalogPage }) },
      { path: '/admin/imports', lazy: async () => ({ Component: (await import('./pages/console')).ImportsPage }) },
      { path: '/admin/users', lazy: async () => ({ Component: (await import('./pages/admin')).UsersPage }) },
      { path: '/admin/users/:userID', lazy: async () => ({ Component: (await import('./pages/admin')).UserDetail }) },
      { path: '/admin/invitations', lazy: async () => ({ Component: (await import('./pages/admin')).InvitationsPage }) },
      { path: '/admin/audit', lazy: async () => ({ Component: (await import('./pages/admin')).AuditPage }) },
    ] },
    { path: '*', Component: NotFound },
  ] },
] }]);
createRoot(document.getElementById('root')!).render(<React.StrictMode><QueryClientProvider client={queryClient}><Suspense fallback={<Loading />}><RouterProvider router={router} /></Suspense></QueryClientProvider></React.StrictMode>);

// Installable app: the worker caches only hashed build files (see public/sw.js).
if ('serviceWorker' in navigator && import.meta.env.PROD) {
  window.addEventListener('load', () => { void navigator.serviceWorker.register('/sw.js').catch(() => undefined); });
}
