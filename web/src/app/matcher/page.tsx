"use client";

import { useMatcherPage } from "./useMatcherPage";
import { MatcherView } from "./MatcherView";

export default function MatcherPage() {
  const { handleRun, handleRetry, ...viewData } = useMatcherPage();

  return (
    <MatcherView {...viewData} onMatch={handleRun} onRetry={handleRetry} />
  );
}
