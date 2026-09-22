/**
 * Howl Ecosystem Shared Client Script - HowlDream
 */

document.addEventListener('DOMContentLoaded', () => {
  // Theme Management
  const themeToggleBtn = document.getElementById('theme-toggle');
  const root = document.documentElement;

  const getPreferredTheme = () => {
    const saved = localStorage.getItem('howl-theme');
    if (saved) return saved;
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  };

  const applyTheme = (theme) => {
    if (theme === 'dark') {
      root.setAttribute('data-theme', 'dark');
      document.body.classList.remove('light-mode');
    } else {
      root.removeAttribute('data-theme');
      document.body.classList.add('light-mode');
    }

    if (themeToggleBtn) {
      themeToggleBtn.setAttribute('aria-pressed', String(theme === 'dark'));
      themeToggleBtn.innerHTML = theme === 'dark'
        ? '<span aria-hidden="true">☼</span> [LIGHT_MODE]'
        : '<span aria-hidden="true">☾</span> [DARK_MODE]';
    }
  };

  const initialTheme = getPreferredTheme();
  applyTheme(initialTheme);

  if (themeToggleBtn) {
    themeToggleBtn.addEventListener('click', () => {
      const current = root.getAttribute('data-theme') === 'dark' ? 'dark' : 'light';
      const next = current === 'dark' ? 'light' : 'dark';
      localStorage.setItem('howl-theme', next);
      applyTheme(next);
    });
  }

  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', (e) => {
    if (!localStorage.getItem('howl-theme')) {
      applyTheme(e.matches ? 'dark' : 'light');
    }
  });

  // Hamburger / Ecosystem Drawer
  const drawerToggle = document.getElementById('eco-menu-toggle');
  const drawer = document.getElementById('eco-drawer');
  const drawerOverlay = document.getElementById('eco-drawer-overlay');
  const drawerCloseBtn = document.getElementById('eco-drawer-close');

  const openDrawer = () => {
    if (drawer && drawerOverlay) {
      drawer.classList.add('active');
      drawerOverlay.classList.add('active');
      drawer.setAttribute('aria-hidden', 'false');
      drawerOverlay.setAttribute('aria-hidden', 'false');
      if (drawerToggle) drawerToggle.setAttribute('aria-expanded', 'true');
      document.body.style.overflow = 'hidden';
      if (drawerCloseBtn) drawerCloseBtn.focus();
    }
  };

  const closeDrawer = () => {
    if (drawer && drawerOverlay) {
      drawer.classList.remove('active');
      drawerOverlay.classList.remove('active');
      drawer.setAttribute('aria-hidden', 'true');
      drawerOverlay.setAttribute('aria-hidden', 'true');
      if (drawerToggle) {
        drawerToggle.setAttribute('aria-expanded', 'false');
        drawerToggle.focus();
      }
      document.body.style.overflow = '';
    }
  };

  if (drawerToggle) {
    drawerToggle.addEventListener('click', (e) => {
      e.stopPropagation();
      const isActive = drawer && drawer.classList.contains('active');
      if (isActive) closeDrawer();
      else openDrawer();
    });
  }

  if (drawerCloseBtn) {
    drawerCloseBtn.addEventListener('click', closeDrawer);
  }

  if (drawerOverlay) {
    drawerOverlay.addEventListener('click', closeDrawer);
  }

  if (drawer) {
    drawer.querySelectorAll('.drawer-node-card').forEach((card) => {
      card.addEventListener('click', () => {
        closeDrawer();
      });
    });
  }

  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && drawer && drawer.classList.contains('active')) {
      closeDrawer();
    }
  });

  // Code Copy Buttons
  document.querySelectorAll('.code-container').forEach((container) => {
    const copyBtn = container.querySelector('.btn-copy');
    const codeEl = container.querySelector('pre code') || container.querySelector('pre');

    if (copyBtn && codeEl) {
      copyBtn.addEventListener('click', async () => {
        try {
          const text = (codeEl.textContent || '').trim();
          let copied = false;
          if (navigator.clipboard && navigator.clipboard.writeText) {
            try {
              await navigator.clipboard.writeText(text);
              copied = true;
            } catch (_) {
              // Permission or headless fallback
            }
          }
          if (!copied) {
            const textarea = document.createElement('textarea');
            textarea.value = text;
            textarea.style.position = 'fixed';
            textarea.style.opacity = '0';
            document.body.appendChild(textarea);
            textarea.select();
            document.execCommand('copy');
            document.body.removeChild(textarea);
            copied = true;
          }
          if (copied) {
            const originalText = copyBtn.textContent;
            copyBtn.textContent = '[COPIED!]';
            copyBtn.style.borderColor = 'var(--color-cyan)';
            copyBtn.style.color = 'var(--color-cyan)';
            setTimeout(() => {
              copyBtn.textContent = originalText;
              copyBtn.style.borderColor = '';
              copyBtn.style.color = '';
            }, 2000);
          }
        } catch (err) {
          console.error('Failed to copy code snippet:', err);
        }
      });
    }
  });
});
