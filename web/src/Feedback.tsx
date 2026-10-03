export function ErrorMessage({ message, retry }: { message: string; retry: () => void }) {
  return <div className="error-row">
    <p className="error" role="alert">{message}</p>
    <button className="button" onClick={retry}>Try again</button>
  </div>;
}
