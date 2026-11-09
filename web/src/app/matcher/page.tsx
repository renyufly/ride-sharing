"use client";

import { useMatcherPage } from "./useMatcherPage";
import { MatcherView } from "./MatcherView";
import { LiveDispatchDemo } from "./LiveDispatchDemo";

export default function MatcherPage() {
  const { handleRun, handleRetry, ...viewData } = useMatcherPage();

  return (
    <>
      <LiveDispatchDemo />
      <MatcherView {...viewData} onMatch={handleRun} onRetry={handleRetry} />
    </>
  );
}
