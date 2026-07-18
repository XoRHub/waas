// @vitest-environment jsdom
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@/test/render';
import type { WorkspaceSchedule } from '@/types';
import { ScheduleEditor } from './ScheduleEditor';

/** The editor is designed for a CONTROLLED parent (all three real call
 * sites are): the harness loops onChange back into value, spying on it. */
function Controlled({
  initial,
  onChange,
}: {
  initial: WorkspaceSchedule | undefined;
  onChange: (v: WorkspaceSchedule | undefined) => void;
}) {
  const [value, setValue] = useState(initial);
  return (
    <ScheduleEditor
      value={value}
      onChange={(v) => {
        setValue(v);
        onChange(v);
      }}
    />
  );
}

describe('ScheduleEditor', () => {
  it('a cron typed character by character keeps its spaces', async () => {
    const onChange = vi.fn();
    renderWithProviders(<Controlled initial={{ timezone: 'Europe/Paris' }} onChange={onChange} />);

    const uptime = screen.getByPlaceholderText('0 8 * * 1-5');
    await userEvent.type(uptime, '0 9 * * 1-5');
    expect(uptime).toHaveValue('0 9 * * 1-5');
    expect(onChange).toHaveBeenLastCalledWith({
      timezone: 'Europe/Paris',
      uptime: ['0 9 * * 1-5'],
    });
  });

  it('re-seeds the drafts when the parent value changes externally', async () => {
    // Same shape as a template switch in the creation dialog: the value
    // prop is replaced from above while the component stays mounted.
    function Harness() {
      const [value, setValue] = useState<WorkspaceSchedule | undefined>({
        uptime: ['0 8 * * 1-5'],
      });
      return (
        <>
          <ScheduleEditor value={value} onChange={setValue} />
          <button type="button" onClick={() => setValue({ uptime: ['0 7 * * *'] })}>
            switch
          </button>
        </>
      );
    }
    renderWithProviders(<Harness />);
    expect(screen.getByDisplayValue('0 8 * * 1-5')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: 'switch' }));
    expect(screen.getByDisplayValue('0 7 * * *')).toBeInTheDocument();
  });

  it('emptying every field reports no schedule at all', async () => {
    const onChange = vi.fn();
    renderWithProviders(
      <Controlled
        initial={{ timezone: 'Europe/Paris', uptime: ['0 8 * * 1-5'] }}
        onChange={onChange}
      />,
    );

    await userEvent.clear(screen.getByDisplayValue('0 8 * * 1-5'));
    await userEvent.clear(screen.getByDisplayValue('Europe/Paris'));
    expect(onChange).toHaveBeenLastCalledWith(undefined);
  });
});
