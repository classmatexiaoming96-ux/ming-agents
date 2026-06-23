import type { StepStatusChange } from '../hooks/useRunStatusSSE';

export interface ToastNotification {
  id: string;
  runId: string;
  change: StepStatusChange;
}

interface NotificationToastProps {
  notifications: ToastNotification[];
  onDismiss: (id: string) => void;
  onOpen: (notification: ToastNotification) => void;
}

export function NotificationToast({
  notifications,
  onDismiss,
  onOpen,
}: NotificationToastProps) {
  if (notifications.length === 0) {
    return null;
  }

  return (
    <div className="toast-stack" role="status" aria-live="polite">
      {notifications.map((notification) => (
        <div key={notification.id} className="toast">
          <button
            className="toast-body"
            type="button"
            onClick={() => onOpen(notification)}
          >
            <span className="toast-title">{notification.change.node}</span>
            <span className="toast-text">
              {notification.change.from} {'->'} {notification.change.to}
            </span>
          </button>
          <button
            className="toast-close"
            type="button"
            aria-label="关闭通知"
            onClick={() => onDismiss(notification.id)}
          >
            x
          </button>
        </div>
      ))}
    </div>
  );
}
