import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import Settings from './Settings';
import { setSetting, useSettings } from '../settings';

afterEach(() => { cleanup(); setSetting('chart', true); setSetting('chartExport', true); });
function Probe() {
  const settings = useSettings();
  return <output>{JSON.stringify(settings)}</output>;
}
it('toggles chart settings and persists them', () => {
  const onClose = vi.fn();
  render(<><Settings onClose={onClose} /><Probe /></>);
  const chart = screen.getByLabelText('Show search chart'), exports = screen.getByLabelText('Show chart downloads (CSV, SVG, PNG)');
  expect(chart).toBeChecked();
  expect(exports).toBeChecked();
  fireEvent.click(chart);
  fireEvent.click(exports);
  expect(screen.getByRole('status')).toHaveTextContent('{"chart":false,"chartExport":false}');
  expect(JSON.parse(localStorage.getItem('zedsearch-settings')!)).toEqual({ chart: false, chartExport: false });
  fireEvent.click(screen.getByRole('button', { name: 'Close' }));
  expect(onClose).toHaveBeenCalledOnce();
});
it('reloads settings changed in another tab', () => {
  render(<Probe />);
  localStorage.setItem('zedsearch-settings', '{"chart":false}');
  fireEvent(window, new StorageEvent('storage', { key: 'zedsearch-settings' }));
  expect(screen.getByRole('status')).toHaveTextContent('{"chart":false,"chartExport":true}');
  localStorage.setItem('zedsearch-settings', 'not json');
  fireEvent(window, new StorageEvent('storage', { key: null }));
  expect(screen.getByRole('status')).toHaveTextContent('{"chart":true,"chartExport":true}');
});
