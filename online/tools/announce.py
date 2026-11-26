#!/usr/bin/env python3
"""Writes updates/announce.json from the form filled in on GitHub (Actions > Run workflow).
Everyone who has the museum app sees it once, the next time they open the app.  Created By ArshiaRM"""
import datetime, json, os, time

msg = os.environ.get('MSG', '').strip()
title = os.environ.get('TITLE', '').strip()
link = os.environ.get('LINK', '').strip()

if msg in ('پاک', 'حذف', 'clear', '-'):
    data = {'id': '', 'title': '', 'text': '', 'link': ''}
    print('announcement removed')
else:
    text = '\n'.join(p.strip() for p in msg.split('|') if p.strip())
    if link and not link.startswith(('https://', 'http://')):
        link = 'https://' + link
    data = {
        'id': f"{int(time.time())}-{os.environ.get('GITHUB_RUN_ID', '0')}",
        'title': title or 'اعلان موزه',
        'text': text,
        'link': link,
        'date': datetime.datetime.now(datetime.timezone.utc).strftime('%Y-%m-%d %H:%M UTC'),
    }
    print('new announcement:', data['title'], '-', text)

os.makedirs('updates', exist_ok=True)
with open('updates/announce.json', 'w', encoding='utf-8') as f:
    json.dump(data, f, ensure_ascii=False, indent=2)
    f.write('\n')
