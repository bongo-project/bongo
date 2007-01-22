import Handler, Mdb, Security, Template, Util
from bongo import MDB

def ShowProxy(req):
    tmpl = Template.Template('tmpl/proxy.tmpl')
    tmpl.SendPage(req)
    return 0
