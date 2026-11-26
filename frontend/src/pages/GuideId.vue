<script setup lang="ts">
import shot from '../assets/guide-memealerts-id.webp'
</script>

<template>
  <section class="card guide">
    <h2>Как найти свой ID на MemeAlerts</h2>
    <p>
      ID нужен, если на канале есть несколько саппортеров с одинаковым ником: ник не подходит однозначно,
      а ID принадлежит только вам. Введите его в награде вместо ника.
    </p>

    <ol class="how">
      <li>
        Откройте страницу
        <a href="https://memealerts.com/settings" target="_blank" rel="noopener">memealerts.com/settings</a>
        (нужно быть авторизованным на MemeAlerts).
      </li>
      <li>Слева внизу, под аватаром, найдите строку <b>ID</b>: набор из 24 символов (цифры и буквы a–f).</li>
      <li>Скопируйте его и введите при получении награды вместо ника.</li>
    </ol>

    <figure>
      <img :src="shot" alt="Страница настроек MemeAlerts: ID указан слева внизу под аватаром" width="1280" height="799" />
      <figcaption>Так выглядит страница настроек: ID слева внизу.</figcaption>
    </figure>

    <p class="muted">Вместо ID можно указать ссылку на свой профиль вида <code>memealerts.com/ваша_ссылка</code>.</p>
    <RouterLink class="btn" to="/">На главную</RouterLink>
  </section>
</template>
