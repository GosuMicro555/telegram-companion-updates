export async function readAutomationRunning(
  fetchDashboard: () => Promise<{ running: boolean }>
): Promise<boolean> {
  const dashboard = await fetchDashboard();
  return Boolean(dashboard.running);
}

export async function startAutomationAfterSavingKeywordSettings(
  save: () => Promise<unknown>,
  start: () => Promise<void>
): Promise<void> {
  await save();
  await start();
}
