import { describe, it, expect, afterEach, vi } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { PhasesLane } from './PhasesLane';
import { api, type InvestigationPhase } from '../../api';
import type { Range } from './timeline/scale';

afterEach(cleanup);

vi.mock('../../api', async () => {
  const actual = await vi.importActual<typeof import('../../api')>('../../api');
  return {
    ...actual,
    api: {
      ...actual.api,
      investigations: {
        ...actual.api.investigations,
        phases: {
          list:   vi.fn().mockResolvedValue([]),
          create: vi.fn().mockResolvedValue({ id: 7 }),
          update: vi.fn().mockResolvedValue(undefined),
          delete: vi.fn().mockResolvedValue(undefined),
        },
      },
    },
  };
});

const range: Range = { tMin: 0, tMax: 1000 };
const baseProps = {
  invID: 1,
  cpInstanceID: undefined as string | undefined,
  range,
  drawableWidth: 1000,
  laneLabelWidth: 80,
  onPhasesChange: vi.fn(),
};

function phase(id: number, name: string, start: number, end: number): InvestigationPhase {
  return {
    ID: id,
    InvestigationID: 1,
    Name: name,
    StartTs: start,
    EndTs: end,
    CreatedBy: { Valid: false, String: '' },
    CreatedAt: '',
  };
}

describe('PhasesLane', () => {
  it('renders empty-state hint when phases is empty', () => {
    render(<PhasesLane {...baseProps} phases={[]} />);
    expect(screen.getByText(/drag here to mark a phase/i)).toBeTruthy();
  });

  it('renders phase pills for each phase', () => {
    render(<PhasesLane {...baseProps} phases={[
      phase(1, 'Initial detection', 100, 300),
      phase(2, 'Containment', 400, 700),
    ]} />);
    expect(screen.getByText('Initial detection')).toBeTruthy();
    expect(screen.getByText('Containment')).toBeTruthy();
  });

  it('clicking a phase pill swaps it for an input', () => {
    render(<PhasesLane {...baseProps} phases={[phase(1, 'old', 100, 300)]} />);
    fireEvent.click(screen.getByText('old'));
    const input = screen.getByDisplayValue('old') as HTMLInputElement;
    expect(input).toBeTruthy();
  });

  it('renaming a phase calls api.update + onPhasesChange', async () => {
    const onChange = vi.fn();
    render(<PhasesLane {...baseProps} onPhasesChange={onChange} phases={[phase(1, 'old', 100, 300)]} />);
    fireEvent.click(screen.getByText('old'));
    const input = screen.getByDisplayValue('old') as HTMLInputElement;
    fireEvent.change(input, { target: { value: 'new' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await new Promise((r) => setTimeout(r, 0));
    expect(api.investigations.phases.update).toHaveBeenCalledWith(1, 1, { name: 'new' }, undefined);
    expect(onChange).toHaveBeenCalled();
  });

  it('Escape on rename input reverts without calling api.update', async () => {
    const updateMock = api.investigations.phases.update as ReturnType<typeof vi.fn>;
    updateMock.mockClear();
    render(<PhasesLane {...baseProps} phases={[phase(1, 'old', 100, 300)]} />);
    fireEvent.click(screen.getByText('old'));
    const input = screen.getByDisplayValue('old') as HTMLInputElement;
    fireEvent.change(input, { target: { value: 'new' } });
    fireEvent.keyDown(input, { key: 'Escape' });
    expect(updateMock).not.toHaveBeenCalled();
    expect(screen.getByText('old')).toBeTruthy();
  });
});
