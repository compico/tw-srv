import { createRouter, createWebHistory } from 'vue-router'
import PanelPage from './pages/PanelPage.vue'
import SupportList from './pages/SupportList.vue'
import SupportNew from './pages/SupportNew.vue'
import SupportTicket from './pages/SupportTicket.vue'

// Все пути отдаёт одна и та же HTML-оболочка (Go), разбор пути на клиенте.
export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', component: PanelPage },
    { path: '/support', component: SupportList },
    { path: '/support/new', component: SupportNew },
    { path: '/support/:id(\\d+)', component: SupportTicket, props: true },
    { path: '/:rest(.*)*', redirect: '/' },
  ],
})
