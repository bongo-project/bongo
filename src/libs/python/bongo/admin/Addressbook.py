import Handler, Mdb, Security, Template, Util
from bongo import MDB

def ShowAddressbook(req):
    tmpl = Template.Template('tmpl/addressbook.tmpl')
    tmpl.SendPage(req)
    return 0
