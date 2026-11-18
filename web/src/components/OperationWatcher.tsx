import { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useStackOperations } from '../api/queries';
import { useNotify } from './Notifications';
import { useWatchedOperations, type Watched } from './OperationPanel';

/**
 * Reports when an operation started in this tab ends while its panel is
 * closed. It follows each stack's operation list, which the live connection
 * refreshes on every change (and polling without it).
 */
export function OperationWatcher() {
  const { watched } = useWatchedOperations();
  const stacks = [...new Set(watched.map((w) => w.stack))];
  return stacks.map((stack) => (
    <StackWatcher
      key={stack}
      stack={stack}
      ops={watched.filter((w) => w.stack === stack)}
    />
  ));
}

function StackWatcher({ stack, ops }: { stack: string; ops: Watched[] }) {
  const { t } = useTranslation();
  const notify = useNotify();
  const { openId, unwatch, open } = useWatchedOperations();
  const list = useStackOperations(stack);
  // Reported once, even if the effect runs again before unwatch applies.
  const reported = useRef(new Set<string>());

  useEffect(() => {
    if (!list.data) return;
    for (const w of ops) {
      const op = list.data.operations.find((o) => o.id === w.id);
      if (!op) {
        // Gone (kept an hour, or the agent restarted), unless the list is
        // older than the operation.
        if (list.dataUpdatedAt > w.since) unwatch(w.id);
        continue;
      }
      if (op.status === 'running' || reported.current.has(w.id)) continue;
      reported.current.add(w.id);
      unwatch(w.id);
      // An open panel shows the result itself.
      if (openId === w.id) continue;
      const action = t(`actions.${w.action}`);
      const title = w.service
        ? t('operation.titleService', { action, stack, service: w.service })
        : t('operation.title', { action, stack });
      const showOutput = {
        label: t('operation.showOutput'),
        onClick: () => open(w),
      };
      if (op.status === 'succeeded') {
        notify(t('operation.notice.succeeded', { title }), 'success');
      } else if (op.status === 'failed') {
        notify(t('operation.notice.failed', { title }), 'error', showOutput);
      } else {
        notify(
          t('operation.notice.canceled', { title }),
          'warning',
          showOutput,
        );
      }
    }
  }, [
    list.data,
    list.dataUpdatedAt,
    ops,
    openId,
    unwatch,
    open,
    notify,
    t,
    stack,
  ]);

  return null;
}
