import { createApp } from 'vue'
import '@fontsource-variable/inter/wght.css'
import '@fontsource-variable/jetbrains-mono/wght.css'
import './styles/app.css'
import App from './App.vue'
import { connect } from './store'
import { startRouter } from './ui'

startRouter()
connect()
createApp(App).mount('#app')
