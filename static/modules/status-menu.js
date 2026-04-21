// Status indicator menu toggle

export function toggleStatusMenu(e) {
  e.stopPropagation();
  document.getElementById('status-menu').classList.toggle('open');
}

export function setupStatusMenu() {
  document.addEventListener('click', () => {
    document.getElementById('status-menu').classList.remove('open');
  });

  document.getElementById('status-indicator').addEventListener('click', toggleStatusMenu);
  document.getElementById('status-menu').addEventListener('click', (e) => e.stopPropagation());
}
