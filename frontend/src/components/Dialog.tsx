import type { FormEvent, ReactNode } from 'react';
import { useEscape } from '@/hooks/useEscape';

/**
 * Modal shell shared by every dialog: Escape closes it (global behavior),
 * the body scrolls on overflow while the title and the footer (cancel/
 * save buttons) stay pinned — action buttons can never be pushed out of
 * view by a long form.
 */
export function Dialog({
  title,
  onClose,
  onSubmit,
  footer,
  children,
  maxWidth = 'max-w-md',
}: {
  title: ReactNode;
  onClose: () => void;
  /** When set, the dialog is a <form> and the footer's submit button works. */
  onSubmit?: (event: FormEvent) => void;
  footer: ReactNode;
  children: ReactNode;
  maxWidth?: string;
}) {
  useEscape(true, onClose);

  const frameClass = `flex max-h-[90vh] w-full ${maxWidth} flex-col rounded-xl bg-white shadow-lg dark:bg-slate-800`;
  const body = (
    <>
      <h2 className="px-6 pb-2 pt-6 text-lg font-semibold text-slate-900 dark:text-white">
        {title}
      </h2>
      <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-6 py-3">{children}</div>
      <div className="flex items-center justify-end gap-2 border-t border-slate-200 px-6 py-4 dark:border-slate-700">
        {footer}
      </div>
    </>
  );

  return (
    <div className="fixed inset-0 z-20 flex items-center justify-center bg-black/40 p-4">
      {onSubmit ? (
        <form onSubmit={onSubmit} className={frameClass}>
          {body}
        </form>
      ) : (
        <div className={frameClass}>{body}</div>
      )}
    </div>
  );
}
