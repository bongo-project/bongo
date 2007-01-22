import Handler, Mdb, Security, Template, Util
from bongo import MDB

def CreateAlias(req):
    if not req.fields.has_key('newaliasname'):
        return Handler.SendMessage(req, 'Alias CN is required')
    if not req.fields.has_key('newaliasref'):
        return Handler.SendMessage(req, 'Aliased object is required')
    aliasname = req.fields.getfirst('newaliasname').value
    aliasref = req.fields.getfirst('newaliasref').value

    attributes = {}
    attributes[req.mdb.A_ALIASED_OBJECT_NAME] = aliasref
    
    context = mdb.default_context
    req.mdb.AddObject(context + '\\' + aliasname, req.mdb.C_ALIAS, attributes)
    return Handler.SendMessage(req, 'Alias created successfully')
