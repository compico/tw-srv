<script setup lang="ts">
import { onMounted } from 'vue'
import Landing from './components/Landing.vue'
import { loadSession, logout, session } from './session'

onMounted(loadSession)

// После входа возвращаем на ту же страницу, где пользователь был.
const loginUrl = () => `/auth/twitch/login?return_to=${encodeURIComponent(location.pathname)}`
</script>

<template>
  <main class="wrap">
    <header class="top">
      <h1><RouterLink to="/" class="home">Мемкоины за баллы канала</RouterLink></h1>
      <div v-if="session.me" class="who">
        <RouterLink class="btn" to="/support">
          Поддержка<span v-if="session.awaiting" class="badge" :title="`Ждут вашего ответа: ${session.awaiting}`">{{ session.awaiting }}</span>
        </RouterLink>
        <img v-if="session.me.twitch.avatarUrl" :src="session.me.twitch.avatarUrl" alt="" />
        <span>{{ session.me.twitch.displayName }}</span>
        <button class="link" @click="logout">Выйти</button>
      </div>
    </header>

    <section v-if="session.phase === 'loading'" class="card muted">Загрузка…</section>

    <Landing v-else-if="session.phase === 'anon'" :login-url="loginUrl()" />

    <section v-else-if="session.phase === 'error'" class="card center">
      <p>Не удалось загрузить данные.</p>
      <button class="btn" @click="loadSession">Повторить</button>
    </section>

    <RouterView v-else />
  </main>
</template>
