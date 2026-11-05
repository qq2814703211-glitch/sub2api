import { describe, expect, it } from 'vitest';
import router from './index';
describe('account operations routes', () => {
    it('registers dashboard, configuration and monitor views behind admin metadata', () => {
        for (const path of ['/admin/ops-center', '/admin/account-ops', '/admin/upstream-monitor']) {
            const r = router.getRoutes().find(x => x.path === path);
            expect(r?.meta.requiresAuth).toBe(true);
            expect(r?.meta.requiresAdmin).toBe(true);
        }
    });
});
