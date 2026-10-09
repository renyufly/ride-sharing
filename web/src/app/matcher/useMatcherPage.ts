import { useState } from "react";
import type { FormEvent } from "react";
import type {
  MatcherRunRequest,
  MatcherRunResponse,
  MatcherErrorResponse,
  MatcherRetryRequest,
} from "@/components/matcher/contracts";
import { DEFAULT_REQUEST, validateForm } from "./matcher_helpers";

// No external route or initialization inputs are needed by the current page.
export function useMatcherPage() {
  // 3. 页面四大核心状态
  const [form, setForm] = useState<MatcherRunRequest>(DEFAULT_REQUEST);
  const [result, setResult] = useState<MatcherRunResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [isRunning, setIsRunning] = useState<boolean>(false);

  //
  const [isRetrying, setIsRetrying] = useState(false);
  const [retryMaxOrdersPerRider, setRetryMaxOrdersPerRider] = useState(0);
  const [retryGracePeriodMs, setRetryGracePeriodMs] = useState(30_000);

  // 4. 表单字段统一更新处理
  const updateField = (
    field: keyof MatcherRunRequest,
    rawValue: string | number,
  ) => {
    setForm((prev) => {
      const next = { ...prev };

      // 判定是否属于数字类型字段
      const isNumberField = typeof DEFAULT_REQUEST[field] === "number";
      const value = isNumberField ? Number(rawValue) || 0 : rawValue;

      (next as Record<string, unknown>)[field] = value;

      // 联动策略切换逻辑
      // 联动策略切换逻辑
      if (field === "strategy") {
        if (value === "nearest") {
          next.topK = 0;
          next.maxExtraDistanceMeters = 0;
          next.maxOrdersPerRider = 0;
        } else if (value === "balanced") {
          if (next.topK === 0) next.topK = 5;
          if (next.maxExtraDistanceMeters === 0) {
            next.maxExtraDistanceMeters = 1500;
          }
          next.maxOrdersPerRider = 0;
        }
      }

      return next;
    });
  };

  // 工程参数预设：仅修改 Workers、Batch Size、Channel Capacity
  const applyPreset = (batchSize: number) => {
    setForm((prev) => ({
      ...prev,
      workers: 2,
      batchSize,
      channelCapacity: 16,
    }));
  };

  // 5. 提交前前端校验

  // 6. 执行实验流程
  const handleRun = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);

    const validationError = validateForm(form);
    if (validationError) {
      setError(validationError);
      return;
    }

    setIsRunning(true);

    // 规范化 Payload：非 balanced 策略强制清零
    const payload: MatcherRunRequest = {
      ...form,
      topK: form.strategy === "balanced" ? form.topK : 0,
      maxExtraDistanceMeters:
        form.strategy === "balanced" ? form.maxExtraDistanceMeters : 0,
      maxOrdersPerRider:
        form.strategy === "balanced" ? form.maxOrdersPerRider : 0,
    };

    try {
      const res = await fetch("/matcher-api/matcher/run", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });

      let data: MatcherRunResponse | MatcherErrorResponse;
      try {
        data = await res.json();
      } catch {
        throw new Error("解析响应失败：服务器未返回有效的 JSON");
      }

      if (!res.ok) {
        const errorMsg =
          "error" in data && data.error ? data.error : "未知服务端异常";
        if (res.status === 400) {
          setError(`参数错误 (400): ${errorMsg}`);
        } else if (res.status === 409) {
          setError(`已有实验正在运行 (409): ${errorMsg}`);
        } else if (res.status === 500) {
          setError(`运行失败 (500): ${errorMsg}`);
        } else {
          setError(`请求失败 (${res.status}): ${errorMsg}`);
        }
        return;
      }

      // 每次新实验成功后，重试参数都会从新实验结果初始化，不会继续使用上一场实验遗留的值
      const response = data as MatcherRunResponse;
      const latestRound = response.rounds?.[response.rounds.length - 1];

      setResult(response);
      setRetryMaxOrdersPerRider(response.config.maxOrdersPerRider);
      setRetryGracePeriodMs(latestRound ? latestRound.gracePeriodMs : 30_000);
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(`无法连接 matcher API: ${msg}`);
    } finally {
      setIsRunning(false);
    }
  };

  // 获取当前轮次
  const currentRound = result?.rounds?.[result.rounds.length - 1] ?? null;

  const canRetry =
    !!result?.runId &&
    !!currentRound &&
    currentRound.deferredTotal > 0 &&
    !isRunning &&
    !isRetrying;

  const handleRetry = async () => {
    if (!result || !currentRound || !canRetry) return;

    setError(null);

    if (
      !Number.isSafeInteger(retryMaxOrdersPerRider) ||
      retryMaxOrdersPerRider < 0
    ) {
      setError("单骑手订单上限必须是非负整数");
      return;
    }

    if (!Number.isSafeInteger(retryGracePeriodMs) || retryGracePeriodMs < 0) {
      setError("宽限时间必须是非负整数（毫秒）");
      return;
    }

    // guard 和参数校验通过后，创建 Retry 请求
    const payload: MatcherRetryRequest = {
      runId: result.runId,
      sourceRound: currentRound.round,
      maxOrdersPerRider: retryMaxOrdersPerRider,
      gracePeriodMs: retryGracePeriodMs,
    };

    setIsRetrying(true);

    try {
      const res = await fetch("/matcher-api/matcher/retry", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify(payload),
      });

      let data: MatcherRunResponse | MatcherErrorResponse;

      try {
        data = await res.json();
      } catch {
        throw new Error("解析响应失败：服务器未返回有效的 JSON");
      }

      if (!res.ok) {
        const message =
          "error" in data && data.error ? data.error : "未知服务端异常";

        if (res.status === 400) {
          setError(`Retry 参数错误 (400): ${message}`);
        } else if (res.status === 404) {
          setError(`运行会话不存在或已被替换 (404): ${message}`);
        } else if (res.status === 409) {
          setError(`当前轮次不可重试或已有实验运行 (409): ${message}`);
        } else if (res.status === 500) {
          setError(`Retry 执行失败 (500): ${message}`);
        } else {
          setError(`Retry 请求失败 (${res.status}): ${message}`);
        }

        return;
      }

      // 服务端返回完整的新状态，直接替换。
      // 如果第 2 轮仍有 Deferred，准备第 3 轮时，输入框就会显示第 2 轮实际采用的参数
      const response = data as MatcherRunResponse;
      const latestRound = response.rounds?.[response.rounds.length - 1];

      setResult(response);

      if (latestRound) {
        setRetryMaxOrdersPerRider(latestRound.maxOrdersPerRider);
        setRetryGracePeriodMs(latestRound.gracePeriodMs);
      }
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err);
      setError(`无法连接 Retry API: ${message}`);
    } finally {
      setIsRetrying(false);
    }
  };

  // 根据策略动态提取纯算法延迟指标与对应标题
  const algorithmLatency = !result
    ? null
    : result.config.strategy === "nearest"
      ? result.performance.algorithmPerformanceMetrics.nearestSearch
      : result.performance.algorithmPerformanceMetrics.algorithmCompute;

  const algorithmLatencyTitle =
    result?.config.strategy === "nearest"
      ? "最近邻搜索"
      : "候选搜索 + 分配决策";

  return {
    form,
    result,
    error,
    isRunning,
    isRetrying,
    retryMaxOrdersPerRider,
    retryGracePeriodMs,
    currentRound,
    canRetry,
    algorithmLatency,
    algorithmLatencyTitle,
    updateField,
    applyPreset,
    handleRun,
    handleRetry,
    changeRetryMaxOrdersPerRider: (value: number) =>
      setRetryMaxOrdersPerRider(value),
    changeRetryGracePeriodMs: (value: number) => setRetryGracePeriodMs(value),
  };
}
