// Shared input styling for the template editor sections. One definition
// for the whole folder — new sections import these instead of pasting
// the class string again (audit-2 C10: the copy count only ever grew).
export const field =
  'mt-1 w-full rounded-md border border-slate-300 px-3 py-2 dark:border-slate-600 dark:bg-slate-700 dark:text-white';
export const fieldSm =
  'mt-0.5 w-full rounded-md border border-slate-300 px-2 py-1.5 text-sm dark:border-slate-600 dark:bg-slate-700 dark:text-white';
