/** @jsxImportSource react */
export function ErrorBanner({ message }: { message: string }) {
  return (
    <div className="rounded-md border border-danger bg-danger-dim px-4 py-3 text-sm text-danger">
      {message}
    </div>
  );
}
