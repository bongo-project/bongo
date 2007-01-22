import Handler, Security, Template, Util, sys, traceback
from bongo import MDB

def GetObjectAttribute(req):
    objdn = req.fields.getfirst('objdn').value
    attrname = req.fields.getfirst('attrname').value
    vals = req.mdb.GetAttributes(objdn.replace('#', '\\'), attrname)
    return Handler.SendList(req, vals)

def SetObjectAttribute(req):
    objdn = req.fields.getfirst('objdn').value
    attrname = req.fields.getfirst('attrname').value
    #value = req.fields.getfirst('value').value
    #for val in req.fileds.getlist('value'):
    #    value = val+','   [value]
    req.mdb.SetAttribute(objdn.replace('#', '\\'), attrname, req.fields.getlist('value'))
    return Handler.SendMessage(req, 'Attribute set successfully')

# This is kind of a stupid method that should get thrown away as soon as
# Bongo's schema is improved.  
def GetAttributeProperty(req):
    response = ''
    objdn = req.fields.getfirst('objdn').value
    attrname = req.fields.getfirst('attrname').value
    propname = req.fields.getfirst('propname').value
    vals = req.mdb.GetAttributes(objdn.replace('#', '\\'), attrname)
    for val in vals:
        if val.startswith(propname+'='):
            response = val.split('=')[-1]
            break
    return Handler.SendMessage(req, response)

def SetAttributeProperty(req):
    response = ''
    objdn = req.fields.getfirst('objdn').value.replace('#','\\')
    attrname = req.fields.getfirst('attrname').value
    propname = req.fields.getfirst('property').value
    value = req.fields.getfirst('value').value
    vals = req.mdb.GetAttributes(objdn,attrname)
    propfound = 'false'
    for i in range(len(vals)):
        if vals[i].startswith(propname+'='):
            vals[i] = propname+'='+value
            propfound = 'true'
    if propfound == 'false':
        vals.append(propname+'='+value)
    req.mdb.SetAttribute(objdn, attrname, vals)
    return Handler.SendMessage(req, 'Attribute property set successfully')

def DeleteObject(req):
    objdn = req.fields.getfirst('objdn').value
    req.mdb.DeleteObject(objdn.replace('#', '\\'))
    return Handler.SendMessage(req, 'User deleted successfully')

def SearchTree(req):
    classname = req.fields.getfirst('class').value
    context = req.mdb.baseDn
    response = _search_tree(req, context, classname)
    return Handler.SendMessage(req, response)

def _search_tree(req, context, classname):
    result = ''
    objlist = req.mdb.EnumerateObjects(context, None)
    for obj in objlist:
        obj = context+'\\'+obj
        details = req.mdb.GetObjectDetails(obj)
        if details['Type'].lower() == classname.lower():
            result = obj
        else:
            result = _search_tree(req, obj, classname)
        if result != '':
            break
    return result

def LoadObjectChildren(req):
    results = []
    context = ''
    if req.fields.has_key('context'):
        context = req.fields.getfirst('context').value
    if context == '':
        context = '#'.join(req.session['username'].split('\\')[0:-1])
    
    objects = req.mdb.EnumerateObjects(context.replace('#', '\\'), None)
    for obj in objects:
        results.append(context + '\\' + obj)
    #if len(results) == 0:
    #    results.append(context)

    html = '<a class="hoverlink" onclick="loadobjchildren(\'%s\');loadobjattrs(\'%s\');">%s</a>'

    # write current object
    response = ''
    #<strong>Current Object</strong><br />'
    currobj = ''
    while context != '' and context.find('#') > -1:
        c_norm = context.replace('#', '\\')
        c_back = context.replace('\\', '#')
        node = context.split('#')[-1]
        if currobj == '':
            currobj = html % (c_back, c_back, node)
        else:
	    currobj = html % (c_back, c_back, node) +'\\'+ currobj
        context = '#'.join(context.split('#')[0:-1])
    response += '\\'+currobj
    response += '<br /><br />'

    # write child objects
    response += '<strong>Child Objects</strong><br />'
    for s in results:
        #s_norm = s.replace('#', '\\')
        s_back = s.replace('\\', '#')
        s_norm = s_back.split('#')[-1]
        response += html % (s_back, s_back, s_norm)
        response += '<br />'
    response += '<br /><br/>'
    return Handler.SendMessage(req, response)

# version 1 - returns attribute list, includes html formating
def xLoadObjectAttributes(req):
    results = []
    if not req.fields.has_key('objdn'):
        objdn = '\\'.join(req.session['username'].split('\\')[0:-1])
        #return Handler.SendMessage(req, 'No object specified')
    else:
        objdn = req.fields.getfirst('objdn').value
        objdn = objdn.replace('#', '\\')
    response = '<strong>Attribute List - %s</strong><br />' % objdn
    response += '<table>'
    html = '<tr><td>%s&nbsp;</td><td><span id="%s">%s</span></td></tr>'
    attributes = req.mdb.EnumerateAttributes(objdn)
    for attr in attributes:
        vals = req.mdb.GetAttributes(objdn, attr)
            
        if vals == None or len(vals) <= 0:
            response += html % (attr,'x',' -ERROR-')
        else:
            for val in vals:
                response += html % (attr, attr, val)
    response += '</table>'
    return Handler.SendMessage(req, response)

# version 2 - returns a dict, enabled for inline editor
def LoadObjectAttributes(req):
    if not req.fields.has_key('objdn'):
        objdn = '\\'.join(req.session['username'].split('\\')[0:-1])
        #return Handler.SendMessage(req, 'No object specified')
    else:
        objdn = req.fields.getfirst('objdn').value
        objdn = objdn.replace('#', '\\')
    attributes = req.mdb.EnumerateAttributes(objdn)

    d = {}
    for attr in attributes:
        vals = req.mdb.GetAttributes(objdn, attr)
        if vals == None or len(vals) <= 0:
            d[attr.replace(' ','_')] = ['error']
        else:
            d[attr.replace(' ','_')] = vals
    return Handler.SendDict(req, d)

# version3 - returns a dict, includes all schema defined attributes
def zLoadObjectAttributes(req):
    """Return a dict of (set and unset) attributes on a BongoUser."""
    if not req.fields.has_key('objdn'):
        objdn = '\\'.join(req.session['username'].split('\\')[0:-1])
        #return Handler.SendMessage(req, 'No object specified')
    else:
        objdn = req.fields.getfirst('objdn').value
        objdn = objdn.replace('#', '\\')

    fields = {}
    details = req.mdb.GetObjectDetails(objdn)
    if 'Type' not in details.keys():
        raise bongo.BongoError('Could not determine type for ' + objdn)
    #fields['len'] = len( Util.GetClassAttributes(details['Type']))
    try:
        for attribute in Util.GetClassAttributes(details['Type']):
            attrs = req.mdb.GetAttributes(objdn, attribute)
            if attrs == None or len(attrs) <= 0:
                fields[str(attribute.replace(' ','_'))] = ''
            else:
                fields[str(attribute.replace(' ','_'))] = attrs
    except Exception, e:
        str(e)
    if len(fields) <= 0:
        attributes = req.mdb.EnumerateAttributes(objdn)
        for attr in attributes:
            vals = req.mdb.GetAttributes(objdn, attr)
            if vals == None or len(vals) <= 0:
                fields[attr.replace(' ','_')] = ['error']
            else:
                fields[attr.replace(' ','_')] = vals
        #for val in details.keys():
        #    fields[val] = details[val]
    else:
        fields['Object_Class'] = req.mdb.GetAttributes(objdn,'Object Class')
    return Handler.SendDict(req,fields)

