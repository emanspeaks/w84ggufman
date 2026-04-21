// Theme initialization and toggle

export function initTheme() {
  if (localStorage.getItem('theme') === 'light')
    document.documentElement.classList.add('light');
}

export function setupThemeToggle() {
  document.getElementById('theme-toggle').addEventListener('click', () => {
    const isLight = document.documentElement.classList.toggle('light');
    localStorage.setItem('theme', isLight ? 'light' : 'dark');
  });
}
