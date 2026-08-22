/**
 * Billing Store — 积分计费系统
 *
 * 管理用户积分余额、消费记录、充值订单和套餐订阅。
 * 通过后端 API /api/v2/billing/* 进行数据交互。
 *
 * 核心概念：
* - 积分（Points）：用户可感知的统一计费单位，与底层 Token 解耦
* - 余额（Balance）：用户当前可用积分
* - 消费（Consumption）：每次写作/编辑/搜索等操作消耗的积分
 * - 套餐（Plan）：月度订阅方案，包含积分额度和功能权限
 */
import { create } from "zustand";
import { useAuthStore } from "@/stores/auth-store";

// ─── 类型定义 ─────────────────────────────────────────

export interface PointBalance {
  point_balance: number;
  total_recharged: number;
  total_consumed: number;
  plan_name: string;
  plan_display_name: string;
  plan_expires_at: string | null;
  features: Record<string, unknown>;
}

export interface ConsumptionLog {
  id: string;
  user_id: string;
  trace_id?: string;
  task_type: string;
  model_name?: string;
  prompt_tokens: number;
  completion_tokens: number;
  input_rate: number;
  output_rate: number;
  points_used: number;
  balance_before: number;
  balance_after: number;
  created_at: string;
}

export interface RechargeOrder {
  id: string;
  user_id: string;
  amount: number;
  point_amount: number;
  payment_method: string;
  payment_url?: string;
  status: "pending" | "paid" | "failed" | "expired";
  paid_at?: string;
  expires_at?: string;
  created_at: string;
}

export interface SubscriptionPlan {
  id: string;
  name: string;
  display_name: string;
  price_monthly: number;
  point_quota: number;
  features: Record<string, unknown>;
  is_active: boolean;
  is_popular: boolean;
  sort_order: number;
}

export interface UserSubscription {
  id: string;
  user_id: string;
  plan_id: string;
  plan_name: string;
  plan_display_name: string;
  status: string;
  started_at: string;
  expires_at?: string;
  auto_renew: boolean;
}

export interface RedeemCode {
  id: string;
  code: string;
  point_amount: number;
  batch_label?: string;
  status: "unused" | "used" | "disabled" | "expired";
  redeemed_by?: string;
  redeemed_at?: string;
  expires_at?: string;
  created_at: string;
}

interface BillingState {
  // 余额
  balance: PointBalance | null;
  balanceLoading: boolean;

  // 消费记录
  consumptionLogs: ConsumptionLog[];
  consumptionTotal: number;
  consumptionLoading: boolean;

  // 充值订单
  rechargeOrders: RechargeOrder[];

  // 套餐
  plans: SubscriptionPlan[];
  currentSubscription: UserSubscription | null;

  // Actions
  loadBalance: () => Promise<void>;
  loadConsumption: (days?: number, page?: number, limit?: number, append?: boolean) => Promise<void>;
  loadConsumptionSummary: (days?: number) => Promise<{ total_consumed: number; by_category: Record<string, number> }>;
  loadRechargeOrders: (limit?: number) => Promise<void>;
  loadPlans: () => Promise<void>;
  loadSubscription: () => Promise<void>;
  subscribe: (planId: string) => Promise<UserSubscription>;
  createRecharge: (pointAmount: number, paymentMethod?: string) => Promise<RechargeOrder>;
  redeem: (code: string) => Promise<{ points: number; message: string }>;
  loadFeatures: () => Promise<Record<string, unknown>>;
}

// ─── 辅助 ─────────────────────────────────────────────

function authHeaders(): Record<string, string> {
  const token = useAuthStore.getState().token;
  return token ? { Authorization: `Bearer ${token}`, "Content-Type": "application/json" } : { "Content-Type": "application/json" };
}

async function apiCall<T>(url: string, options?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    ...options,
    headers: { ...authHeaders(), ...(options?.headers || {}) },
  });
  const json = await res.json();
  if (!json.success) throw new Error(json.error?.message || "API error");
  return json.data as T;
}

// ─── Store ─────────────────────────────────────────────

export const useBillingStore = create<BillingState>((set, get) => ({
  balance: null,
  balanceLoading: false,
  consumptionLogs: [],
  consumptionTotal: 0,
  consumptionLoading: false,
  rechargeOrders: [],
  plans: [],
  currentSubscription: null,

  loadBalance: async () => {
    set({ balanceLoading: true });
    try {
      const data = await apiCall<PointBalance>("/api/v2/billing/balance");
      set({ balance: data, balanceLoading: false });
    } catch {
      set({ balanceLoading: false });
    }
  },

  loadConsumption: async (days = 30, page = 1, limit = 20, append = false) => {
    set({ consumptionLoading: true });
    try {
      const data = await apiCall<{ items: ConsumptionLog[]; total: number }>(
        `/api/v2/billing/consumption?days=${days}&page=${page}&limit=${limit}`,
      );
      set((state) => ({
        consumptionLogs: append ? [...state.consumptionLogs, ...(data.items || [])] : (data.items || []),
        consumptionTotal: data.total,
        consumptionLoading: false,
      }));
    } catch {
      set({ consumptionLoading: false });
    }
  },

  loadConsumptionSummary: async (days = 30) => {
    return apiCall(`/api/v2/billing/consumption/summary?days=${days}`);
  },

  loadRechargeOrders: async (limit = 20) => {
    try {
      const data = await apiCall<{ orders: RechargeOrder[] }>(`/api/v2/billing/recharge/orders?limit=${limit}`);
      set({ rechargeOrders: data.orders || [] });
    } catch {
      // silent
    }
  },

  loadPlans: async () => {
    try {
      const data = await apiCall<{ plans: SubscriptionPlan[] }>("/api/v2/billing/plans");
      set({ plans: data.plans || [] });
    } catch {
      // silent
    }
  },

  loadSubscription: async () => {
    try {
      const data = await apiCall<UserSubscription>("/api/v2/billing/subscription");
      set({ currentSubscription: data });
    } catch {
      // silent
    }
  },

  subscribe: async (planId) => {
    const data = await apiCall<UserSubscription>("/api/v2/billing/subscribe", {
      method: "POST",
      body: JSON.stringify({ plan_id: planId }),
    });
    // 订阅成功后刷新余额
    get().loadBalance();
    set({ currentSubscription: data });
    return data;
  },

  createRecharge: async (pointAmount, paymentMethod = "manual") => {
    const data = await apiCall<RechargeOrder>("/api/v2/billing/recharge", {
      method: "POST",
      body: JSON.stringify({ point_amount: pointAmount, payment_method: paymentMethod }),
    });
    // 刷新订单列表
    get().loadRechargeOrders();
    return data;
  },

  redeem: async (code) => {
    const data = await apiCall<{ points: number; message: string }>("/api/v2/billing/redeem", {
      method: "POST",
      body: JSON.stringify({ code }),
    });
    // 兑换成功后刷新余额
    get().loadBalance();
    return data;
  },

  loadFeatures: async () => {
    return apiCall("/api/v2/billing/features");
  },
}));
