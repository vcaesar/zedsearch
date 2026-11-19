import { setSetting, useSettings } from '../settings';
import { Modal } from './management/Common';

export default function Settings({ onClose }: { onClose: () => void }) {
  const settings = useSettings();
  return <Modal title="Settings" onClose={onClose}>
    <div className="grid gap-4">
      <label><input type="checkbox" checked={settings.chart} onChange={event => setSetting('chart', event.target.checked)} /> Show search chart</label>
      <label><input type="checkbox" checked={settings.chartExport} onChange={event => setSetting('chartExport', event.target.checked)} /> Show chart downloads (CSV, SVG, PNG)</label>
    </div>
  </Modal>;
}
