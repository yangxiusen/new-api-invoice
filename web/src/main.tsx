import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
  Navigate,
} from "@tanstack/react-router";
import { Workspace } from "./components/workspace";
import { OrdersPage } from "./features/orders";
import { TitlesPage } from "./features/titles";
import { ApplicationsPage } from "./features/applications";
import { UsersPage } from "./features/users";
import { OperationsPage } from "./features/operations";
import "./styles/tokens.css";
import { parseSearch, stringifySearch } from "./lib/search";

const root = createRootRoute({ component: Workspace });
const index = createRoute({
  getParentRoute: () => root,
  path: "/",
  component: () => <Navigate to="/orders" replace />,
});
const orders = createRoute({ getParentRoute: () => root, path: "/orders", component: OrdersPage });
const apps = createRoute({
  getParentRoute: () => root,
  path: "/applications",
  component: () => <ApplicationsPage />,
});
const titles = createRoute({ getParentRoute: () => root, path: "/titles", component: TitlesPage });
const users = createRoute({
  getParentRoute: () => root,
  path: "/admin/users",
  component: UsersPage,
});
const adminApps = createRoute({
  getParentRoute: () => root,
  path: "/admin/applications",
  validateSearch: (s: Record<string, unknown>) => ({
    user_id: typeof s.user_id === "string" ? s.user_id : "",
  }),
  component: () => {
    const s = adminApps.useSearch();
    return <ApplicationsPage key={s.user_id} admin userID={s.user_id} />;
  },
});
const ops = createRoute({
  getParentRoute: () => root,
  path: "/admin/operations",
  validateSearch: (s: Record<string, unknown>) => ({
    target_user_id: typeof s.target_user_id === "string" ? s.target_user_id : "",
  }),
  component: () => {
    const s = ops.useSearch();
    return <OperationsPage key={s.target_user_id} targetID={s.target_user_id} />;
  },
});
const router = createRouter({
  routeTree: root.addChildren([index, orders, apps, titles, users, adminApps, ops]),
  basepath: "/invoice",
  parseSearch,
  stringifySearch,
  defaultPreload: "intent",
  defaultNotFoundComponent: () => (
    <div className="panel access">
      <h1>页面不存在</h1>
      <a href="/invoice/applications">返回我的申请</a>
    </div>
  ),
});
declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
const client = new QueryClient({
  defaultOptions: {
    queries: { retry: false, staleTime: 0, gcTime: 60000 },
    mutations: { retry: false },
  },
});
createRoot(document.getElementById("root")!).render(
  <QueryClientProvider client={client}>
    <RouterProvider router={router} />
  </QueryClientProvider>,
);
