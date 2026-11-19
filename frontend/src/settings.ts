import { useSyncExternalStore } from 'react';

export type Settings = { chart: boolean; chartExport: boolean };
const storageKey = 'zedsearch-settings';
const defaults: Settings = { chart: true, chartExport: true };
function readSettings(): Settings {
  try {
    const value = JSON.parse(localStorage.getItem(storageKey) || '{}') as Partial<Settings>;
    return { chart: value.chart ?? defaults.chart, chartExport: value.chartExport ?? defaults.chartExport };
  } catch {
    return defaults;
  }
}
let settings = readSettings();
const listeners = new Set<() => void>();
const notify = () => listeners.forEach(listener => listener());
export function setSetting<K extends keyof Settings>(key: K, value: Settings[K]) {
  settings = { ...settings, [key]: value };
  try {
    localStorage.setItem(storageKey, JSON.stringify(settings));
  } catch {
    // Keep the setting usable when browser storage is unavailable.
  }
  notify();
}
window.addEventListener('storage', event => {
  if (event.key === storageKey || event.key === null) {
    settings = readSettings();
    notify();
  }
});
function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
export function useSettings() {
  return useSyncExternalStore(subscribe, () => settings);
}
