import Handler, Mdb, Security, Template, Util
from bongo import MDB

def ShowStore(req):
    tmpl = Template.Template('tmpl/store.tmpl')
    tmpl.SendPage(req)
    return 0
