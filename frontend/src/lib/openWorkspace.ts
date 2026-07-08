// openWorkspace applies the user's tab preference for one workspace.
export function openWorkspace(url: string, newTab: boolean, navigate: (to: string) => void) {
  if (newTab) {
    window.open(url, '_blank');
  } else {
    navigate(url);
  }
}
