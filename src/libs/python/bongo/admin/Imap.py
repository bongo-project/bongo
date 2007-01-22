import Handler, Mdb, Security, Template, Util
from bongo import MDB

def ShowImap(req):
    tmpl = Template.Template('tmpl/imap.tmpl')
    tmpl.SendPage(req)
    return 0
