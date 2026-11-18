import { screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import type { StackDetail } from '../api/client';
import { renderWithProviders } from '../test/render';
import { FileAccessHint } from './FileAccessHint';

function stack(overrides: Partial<StackDetail> = {}): StackDetail {
  return {
    name: 'romm',
    managed: true,
    editable: false,
    deletable: false,
    status: 'running',
    servicesTotal: 1,
    servicesRunning: 1,
    path: '/opt/stacks/romm',
    services: [],
    volumes: [],
    dockerAvailable: true,
    warnings: [],
    fileAccess: {
      user: 'gantry',
      missing: [
        { path: '/opt/stacks/romm', access: 'write' },
        { path: '/opt/stacks/romm/.env', access: 'read' },
      ],
    },
    ...overrides,
  };
}

it('names what is missing and the command that allows it', () => {
  renderWithProviders(<FileAccessHint stack={stack()} />);
  expect(screen.getByText(/runs as user gantry/)).toBeInTheDocument();
  expect(screen.getByText('write /opt/stacks/romm')).toBeInTheDocument();
  expect(screen.getByText('read /opt/stacks/romm/.env')).toBeInTheDocument();
  expect(screen.getByText(/can't be changed here/)).toBeInTheDocument();
  expect(screen.getByTestId('chown-command')).toHaveTextContent(
    'sudo chown gantry /opt/stacks/romm /opt/stacks/romm/.env',
  );
});

it('has no command for a read-only file system', () => {
  renderWithProviders(
    <FileAccessHint
      stack={stack({
        fileAccess: {
          user: 'gantry',
          missing: [
            {
              path: '/opt/stacks/romm',
              access: 'write',
              readOnlyFilesystem: true,
            },
          ],
        },
      })}
    />,
  );
  expect(screen.getByText(/read-only file system\)/)).toBeInTheDocument();
  expect(screen.queryByTestId('chown-command')).not.toBeInTheDocument();
  expect(screen.getByText(/A new owner doesn't help/)).toBeInTheDocument();
});

it('says only that deleting is blocked when editing works', () => {
  renderWithProviders(
    <FileAccessHint
      stack={stack({
        editable: true,
        fileAccess: {
          user: 'gantry',
          missing: [{ path: '/opt/stacks', access: 'write' }],
        },
      })}
    />,
  );
  expect(screen.getByText(/can't be deleted here/)).toBeInTheDocument();
  expect(screen.queryByText(/can't be changed here/)).not.toBeInTheDocument();
});

it('shows nothing when nothing is missing or for external stacks', () => {
  const { container } = renderWithProviders(
    <>
      <FileAccessHint
        stack={stack({
          editable: true,
          deletable: true,
          fileAccess: { user: 'gantry', missing: [] },
        })}
      />
      <FileAccessHint
        stack={stack({ managed: false, fileAccess: undefined })}
      />
    </>,
  );
  expect(container).toBeEmptyDOMElement();
});
