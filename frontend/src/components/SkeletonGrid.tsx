// SkeletonGrid mirrors the card layout while the list loads, so the page
// doesn't jump when real cards replace it.
export function SkeletonGrid({ count = 6 }: { count?: number }) {
  return (
    <div className="grid animate-pulse gap-4 sm:grid-cols-2 lg:grid-cols-3" aria-hidden>
      {Array.from({ length: count }, (_, i) => (
        <div
          key={i}
          className="flex flex-col gap-3 rounded-xl bg-white p-5 shadow-sm dark:bg-slate-800"
        >
          <div className="flex items-start justify-between">
            <div className="space-y-2">
              <div className="h-4 w-32 rounded bg-slate-200 dark:bg-slate-700" />
              <div className="h-3 w-24 rounded bg-slate-100 dark:bg-slate-700/60" />
            </div>
            <div className="h-5 w-16 rounded-full bg-slate-100 dark:bg-slate-700/60" />
          </div>
          <div className="mt-auto flex gap-2">
            <div className="h-8 flex-1 rounded-md bg-slate-200 dark:bg-slate-700" />
            <div className="h-8 w-16 rounded-md bg-slate-100 dark:bg-slate-700/60" />
            <div className="h-8 w-16 rounded-md bg-slate-100 dark:bg-slate-700/60" />
          </div>
        </div>
      ))}
    </div>
  );
}
