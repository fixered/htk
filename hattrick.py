import asyncio
import cloudscraper
from bs4 import BeautifulSoup
from playwright.async_api import async_playwright
from urllib.parse import urlparse, parse_qs

URL_PRINCIPALE = "https://hattrick.ws//"
LOGO = "https://www.tuttotech.net/wp-content/uploads/2020/08/NOW-TV-Sky-On-Demand-logo-2.png"


# -----------------------------------------------------------
# 1️⃣ Scarica HTML con Cloudscraper e raccogli tutti i .htm
# -----------------------------------------------------------

def estrai_canali():
    scraper = cloudscraper.create_scraper()
    html = scraper.get(URL_PRINCIPALE).text
    soup = BeautifulSoup(html, "html.parser")

    canali = []
    for btn in soup.find_all("button"):
        a = btn.find("a")
        if a and a.get("href", "").endswith(".htm"):
            canali.append({
                "nome": a.text.strip(),
                "url": a["href"]
            })
    return canali


# -----------------------------------------------------------
# 2️⃣ Funzione sniff per monitorare richieste tipo il TUO SCRIPT
# -----------------------------------------------------------

async def sniff_network(context, streams):
    def on_request(req):
        url = req.url.lower()
        if ".m3u8" in url:
            print("✔️ M3U8:", req.url)
            streams.append(req.url)
        if ".ts" in url:
            print("✔️ TS:", req.url)
            streams.append(req.url)

    context.on("request", on_request)


# -----------------------------------------------------------
# 3️⃣ Playwright migliorato con ricerca iframe "planetary"
# -----------------------------------------------------------

async def apri_e_estrai_iframe(url):

    async with async_playwright() as p:
        browser = await p.webkit.launch(headless=True)

        context = await browser.new_context(
            user_agent="Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) "
                       "AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/604.1"
        )

        streams = []
        await sniff_network(context, streams)

        page = await context.new_page()

        try:
            await page.goto(url, wait_until="load", timeout=60000)
        except:
            print(f"⚠ Timeout nel caricamento di {url}, continuo comunque…")

        await asyncio.sleep(3)

        # 🔎 STAMPA TUTTI GLI IFRAME COME NEL TUO SCRIPT
        print("\n📌 IFRAME TROVATI:")
        for f in page.frames:
            print(" -", f.url)

        # 🔥 PRIORITÀ ASSOLUTA: planetary.lovecdn.ru
        for f in page.frames:
            if "planetary.lovecdn.ru" in f.url:
                return f.url

        # 🔍 Cerca iframe contenenti token
        for f in page.frames:
            if "embed" in f.url or "token=" in f.url:
                return f.url

        # 🔁 Tentativo da DOM
        handles = await page.query_selector_all("iframe")
        for h in handles:
            src = await h.get_attribute("src")
            if src:
                if "planetary" in src or "token=" in src:
                    return src

        return None


# -----------------------------------------------------------
# 4️⃣ Costruisci due flussi: originale + veloce
# -----------------------------------------------------------

def costruisci_flussi(iframe_url):
    if not iframe_url:
        return None, None, None

    parsed = urlparse(iframe_url)
    qs = parse_qs(parsed.query)
    token = qs.get("token", [None])[0]

    if not token:
        return None, None, None

    base_path = parsed.path.rsplit("/", 1)[0]
    canonical = f"{base_path}/index.fmp4.m3u8?token={token}"

    lento = f"{parsed.scheme}://{parsed.netloc}{canonical}"
    veloce = f"https://planetary.lovecdn.ru{canonical}"

    return canonical, lento, veloce


# -----------------------------------------------------------
# 5️⃣ Scrivi i file M3U8
# -----------------------------------------------------------

def scrivi_m3u8(nome_file, entries, usa_veloce=False):
    with open(nome_file, "w", encoding="utf-8") as f:
        f.write("#EXTM3U\n\n")

        for i, canale in enumerate(entries, start=1):
            url = canale["veloce"] if usa_veloce else canale["lento"]
            f.write(
                f'#EXTINF:-1 tvg-id="{i}" group-title="Sky Sport IPTV" tvg-logo="{LOGO}", {canale["nome"]}\n'
            )
            f.write(url + "\n\n")


# -----------------------------------------------------------
# 6️⃣ MAIN
# -----------------------------------------------------------

async def main():

    print("📥 Raccolgo lista canali…")
    lista_canali = estrai_canali()

    risultati = {}
    finali = []

    print("🎬 Analizzo ogni canale…")

    for c in lista_canali:
        print(f"\n➡ ANALIZZO: {c['nome']}")

        iframe = await apri_e_estrai_iframe(c["url"])
        canonical, lento, veloce = costruisci_flussi(iframe)

        if not canonical:
            print("   ⚠ Nessun token trovato")
            continue

        if canonical not in risultati:
            risultati[canonical] = True
            finali.append({
                "nome": c["nome"],
                "canonical": canonical,
                "lento": lento,
                "veloce": veloce
            })
        else:
            print("   🔁 Duplicato ignorato")

    #print("\n📄 Creo hattricklento.m3u8…")
    #scrivi_m3u8("hattricklento.m3u8", finali, usa_veloce=False)

    print("📄 Creo hattrickveloce.m3u8…")
    scrivi_m3u8("hattrickveloce.m3u8", finali, usa_veloce=True)

    print("\n🎉 COMPLETATO!")
    print("   • hattricklento.m3u8")
    print("   • hattrickveloce.m3u8")


# Avvio
asyncio.run(main())