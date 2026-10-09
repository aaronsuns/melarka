import { useEffect } from "react";
import { BRAND } from "./brand";
import { Navigate, NavLink, Outlet, Route, Routes } from "react-router";
import { startOffline } from "./offline";
import { hasNative } from "./native/bridge";
import { AuthProvider, useAuth } from "./auth/AuthProvider";
import LoginPage from "./auth/LoginPage";
import { DownloadsProvider } from "./downloads/DownloadsProvider";
import { EpisodesProvider } from "./channels/EpisodesProvider";
import { ShellPlayer } from "./player/ShellPlayer";
import { PreviewProvider } from "./channels/PreviewProvider";
import { useChannelsEnabled } from "./channels/channelsSwitch";
import { useUnplayed } from "./channels/useUnplayed";
import { PlayerProvider } from "./player/PlayerProvider";
import { PrefsProvider, usePrefs } from "./prefs/PrefsProvider";
import { useT } from "./i18n/i18n";
import type { User } from "./api/types";
import HomePage from "./pages/HomePage";
import SearchPage from "./pages/SearchPage";
import LibraryPage from "./pages/LibraryPage";
import AlbumPage from "./pages/AlbumPage";
import ArtistPage from "./pages/ArtistPage";
import TagPage from "./pages/TagPage";
import PlaylistsPage from "./pages/PlaylistsPage";
import PlaylistPage from "./pages/PlaylistPage";
import SettingsPage from "./pages/SettingsPage";
import DownloadsPage from "./pages/DownloadsPage";
import MyDownloadsPage from "./pages/MyDownloadsPage";
import RecommendationsPage from "./pages/RecommendationsPage";
import ChangePassword from "./pages/ChangePassword";
import AdminLayout from "./pages/admin/AdminLayout";
import UsersPage from "./pages/admin/UsersPage";
import PendingPage from "./pages/admin/PendingPage";
import AllDownloadsPage from "./pages/admin/AllDownloadsPage";
import TrashPage from "./pages/admin/TrashPage";
import LibrariesPage from "./pages/admin/LibrariesPage";
import SystemPage from "./pages/admin/SystemPage";
import ChannelsPage from "./pages/channels/ChannelsPage";
import AddChannelPage from "./pages/channels/AddChannelPage";
import ChannelPage from "./pages/channels/ChannelPage";
import EpisodePage from "./pages/channels/EpisodePage";
import VideoPage from "./pages/video/VideoPage";
import WatchPage from "./pages/video/WatchPage";

function NotFound() {
  const t = useT();
  return <h1 className="page-title">{t("app.notFound")}</h1>;
}


function Shell() {
  const t = useT();
  const channelsOn = useChannelsEnabled();
  const unplayed = useUnplayed(channelsOn);
  return (
    <div className="shell">
      <main className="content"><Outlet /></main>
      <ShellPlayer />
      <nav className={channelsOn ? "tabs tabs-6" : "tabs"}>
        <NavLink to="/" end>{t("nav.home")}</NavLink>
        <NavLink to="/search">{t("nav.search")}</NavLink>
        <NavLink to="/library">{t("nav.library")}</NavLink>
        {/* The count is the tab's description, not its name: the tab stays "频道" to find. */}
        {channelsOn && (
          <NavLink to="/channels" aria-describedby={unplayed > 0 ? "channels-unplayed" : undefined}>
            {t("nav.channels")}
            {unplayed > 0 && (
              <>
                <span className="tab-badge" aria-hidden="true">{unplayed > 99 ? "99+" : unplayed}</span>
                <span id="channels-unplayed" hidden>{t("channels.unplayed", { count: unplayed })}</span>
              </>
            )}
          </NavLink>
        )}
        {channelsOn && <NavLink to="/video">{t("nav.video")}</NavLink>}
        <NavLink to="/settings">{t("nav.me")}</NavLink>
      </nav>
    </div>
  );
}

function Signed({ user }: { user: User }) {
  const { prefs, loaded } = usePrefs();
  // The offline cache follows the signed-in user (their favorites). Inside
  // the iPhone app native keeps the cache: the web's stays off.
  useEffect(() => (hasNative() ? undefined : startOffline()), [user.id]);
  if (!loaded) return <div className="splash">{BRAND}</div>;
  return (
    // Keyed by user: a different account gets a fresh player (and its own
    // pending-events buffer), never the previous user's state. on_open is
    // only read when the player mounts, so changing it in Settings takes
    // effect the next time Lark opens.
    <PlayerProvider key={user.id} userId={user.id} onOpen={prefs.on_open} carLyrics={prefs.car_lyrics !== false} loudness={prefs.normalize_loudness !== false}>
      <EpisodesProvider userId={user.id}>
      <DownloadsProvider>
      <PreviewProvider>
      <Routes>
        <Route element={<Shell />}>
          <Route index element={<HomePage />} />
          <Route path="search" element={<SearchPage />} />
          <Route path="library" element={<LibraryPage />} />
          <Route path="albums/:id" element={<AlbumPage />} />
          <Route path="artists/:id" element={<ArtistPage />} />
          <Route path="tags/:name" element={<TagPage />} />
          <Route path="playlists" element={<PlaylistsPage />} />
          <Route path="playlists/:id" element={<PlaylistPage />} />
          <Route path="downloads" element={<DownloadsPage />} />
          <Route path="my-downloads" element={<MyDownloadsPage />} />
          <Route path="recommendations" element={<RecommendationsPage />} />
          <Route path="channels" element={<ChannelsPage />} />
          <Route path="channels/add" element={<AddChannelPage />} />
          <Route path="channels/:id" element={<ChannelPage />} />
          <Route path="episodes/:id" element={<EpisodePage />} />
          <Route path="video" element={<VideoPage />} />
          <Route path="watch/:id" element={<WatchPage />} />
          <Route path="settings" element={<SettingsPage />} />
          <Route path="change-password" element={<ChangePassword />} />
          <Route path="admin" element={<AdminLayout />}>
            <Route index element={<Navigate to="users" replace />} />
            <Route path="users" element={<UsersPage />} />
            <Route path="pending" element={<PendingPage />} />
            <Route path="downloads" element={<AllDownloadsPage />} />
            <Route path="trash" element={<TrashPage />} />
            <Route path="libraries" element={<LibrariesPage />} />
            <Route path="system" element={<SystemPage />} />
          </Route>
          <Route path="*" element={<NotFound />} />
        </Route>
      </Routes>
      </PreviewProvider>
      </DownloadsProvider>
      </EpisodesProvider>
    </PlayerProvider>
  );
}

function Gate() {
  const { user, loading } = useAuth();
  if (loading) return <div className="splash">{BRAND}</div>;
  if (!user) return <LoginPage />;
  return (
    <PrefsProvider key={user.id}>
      <Signed user={user} />
    </PrefsProvider>
  );
}

export default function App() {
  return (
    <AuthProvider>
      <Gate />
    </AuthProvider>
  );
}
