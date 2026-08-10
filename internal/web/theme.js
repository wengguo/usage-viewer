(() => {
  'use strict';

  const byID = (id) => document.getElementById(id);
  const toggleButtons = document.querySelectorAll('[data-theme-toggle]');

  const isDark = () => document.documentElement.classList.contains('dark');

  const updateLabels = () => {
    const dark = isDark();
    toggleButtons.forEach((button) => {
      button.setAttribute('aria-pressed', String(dark));
      const label = button.querySelector('[data-theme-label]');
      if (label) label.textContent = dark ? '深色模式' : '浅色模式';
    });
  };

  const setTheme = (dark) => {
    document.documentElement.classList.toggle('dark', dark);
    localStorage.setItem('theme', dark ? 'dark' : 'light');
    updateLabels();
  };

  toggleButtons.forEach((button) => {
    button.addEventListener('click', () => setTheme(!isDark()));
  });

  updateLabels();

  const sidebar = byID('app-sidebar');
  const sidebarOverlay = byID('sidebar-overlay');
  const sidebarOpenButton = byID('sidebar-open');
  const sidebarCloseButton = byID('sidebar-close');

  const openSidebar = () => {
    if (!sidebar) return;
    sidebar.classList.remove('-translate-x-full');
    if (sidebarOverlay) sidebarOverlay.hidden = false;
  };
  const closeSidebar = () => {
    if (!sidebar) return;
    sidebar.classList.add('-translate-x-full');
    if (sidebarOverlay) sidebarOverlay.hidden = true;
  };

  if (sidebarOpenButton) sidebarOpenButton.addEventListener('click', openSidebar);
  if (sidebarCloseButton) sidebarCloseButton.addEventListener('click', closeSidebar);
  if (sidebarOverlay) sidebarOverlay.addEventListener('click', closeSidebar);
})();
