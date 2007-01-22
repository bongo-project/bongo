import Handler, Mdb, Security, Template, Util
from bongo import MDB
from bongo.libs import msgapi

def CreateUser(req):
    if not req.fields.has_key('newusername'):
        return Handler.SendMessage(req, 'Username is required')
    if not req.fields.has_key('newfirstname'):
        return Handler.SendMessage(req, 'First name is required')
    if not req.fields.has_key('newlastname'):
        return Handler.SendMessage(req, 'Last name is required')
    if not req.fields.has_key('newemail'):
        return Handler.SendMessage(req, 'Email address is required')
    if not req.fields.has_key('newpassword'):
        return Handler.SendMessage(req, 'Password is requried')
    if not req.fields.has_key('newpassword2'):
        return Handler.SendMessage(req, 'Please confirm your password')

    username = req.fields.getfirst('newusername').value
    firstname = req.fields.getfirst('newfirstname').value
    lastname = req.fields.getfirst('newlastname').value
    email = req.fields.getfirst('newemail').value
    password = req.fields.getfirst('newpassword').value
    password2 = req.fields.getfirst('newpassword2').value

    if password != password2:
        return Handler.SendMessage(req, 'Passwords do not match')

    attributes = {}
    attributes[req.mdb.A_GIVEN_NAME] = [firstname]
    attributes[req.mdb.A_SURNAME] = [lastname]
    attributes[req.mdb.A_EMAIL_ADDRESS] = [email]

    context = req.default_context
    Util.AddBongoUser(req.mdb, context, username, attributes, password)
    return Handler.SendMessage(req, 'User created successfully')

def SearchUsers(req):
    response = ''
    
    if req.fields.has_key('pattern'):
        pattern = req.fields.getfirst('pattern').value
        response += '<table class="usersearch"><tr><th>Name</th></tr>'
        context = req.default_context
        # temporary handling for alias objects;
        # the mdb file driver does not dereference alias objects
        #
        # FIXME: Attribute 'Aliased Object Name' does not exist in
        #        xplschema.h or msgapi.h.  Disabling this feature until
        #        this is resolved.
        #
        #aliasobjs = req.mdb.EnumerateObjects(context, "Alias", pattern)
        #for alias in aliasobjs:
        #    refs = req.mdb.GetDNAttributes(context + '\\' + alias, \
        #                                   'Aliased Object Name')
        #    if len(refs) > 0:
        #        ref = '\\'.join(refs[0].split('\\')[0:-1])
        #        response += _get_search_result_html(req.mdb, context, ref)

        # user objects
        userobjs = req.mdb.EnumerateObjects(context, "BongoUser", pattern)
        for user in userobjs:
            response += _get_search_result_html(req.mdb, context, user)
        response += "</table>";

    return Handler.SendMessage(req, response)

def _get_search_result_html(mdb, context, cn):
    fullname = mdb.GetAttributes(context + '\\' + cn, mdb.A_FULL_NAME)
    firstname = mdb.GetAttributes(context + '\\' + cn, mdb.A_GIVEN_NAME)
    lastname = mdb.GetAttributes(context + '\\' + cn, mdb.A_SURNAME)

    if len(fullname) > 0:
        name = fullname[0]
    elif len(firstname) > 0:
        name = firstname[0]
        if len(lastname) > 0:
            name += ' ' + lastname[0]
    else:
        name = cn

    html = '<tr><td onclick="getObjData(\'%s\');">%s</td></tr>'
    result = html % (context.replace('\\', '#') + '#' +  cn, name)
    return result

def GetUser(req):
    userdn = req.fields.getfirst('userdn').value.replace('#', '\\')
    mdb = req.mdb

    userobj = {}
    userobj['given_name'] = mdb.GetFirstAttribute(userdn, mdb.A_GIVEN_NAME, '')
    userobj['surname'] = mdb.GetFirstAttribute(userdn, mdb.A_SURNAME, '')
    userobj['email_address'] = \
            mdb.GetFirstAttribute(userdn, mdb.A_EMAIL_ADDRESS, '')
    userobj['street_address'] = \
            mdb.GetFirstAttribute(userdn, mdb.A_STREET_ADDRESS, '')
    userobj['locality_name'] = \
            mdb.GetFirstAttribute(userdn, mdb.A_LOCALITY_NAME, '')
    userobj['state_or_province_name'] = \
            mdb.GetFirstAttribute(userdn, mdb.A_STATE_OR_PROVINCE_NAME, '')
    userobj['postal_code'] = \
            mdb.GetFirstAttribute(userdn, mdb.A_POSTAL_CODE, '')
    userobj['telephone_number'] = \
            mdb.GetFirstAttribute(userdn, mdb.A_TELEPHONE_NUMBER, '')
    userobj['bongo_messaging_disabled'] = \
            mdb.GetFirstAttribute(userdn, msgapi.A_MESSAGING_DISABLED, '')
    userobj['bongo_timeout'] = \
            mdb.GetFirstAttribute(userdn, msgapi.A_TIMEOUT, '')
    userobj['bongo_email_address'] = \
            mdb.GetFirstAttribute(userdn, msgapi.A_EMAIL_ADDRESS, '')
    userobj['bongo_quota_value'] = \
            req.mdb.GetFirstAttribute(userdn, msgapi.A_QUOTA_VALUE, '')

    features = req.mdb.GetAttributes(userdn, msgapi.A_FEATURE_SET)
    feature_line = '                                         '
    for line in features:
        if line.startswith('A'):
            feature_line = line
            break

    userobj['feature_imap'] = feature_line[1]
    userobj['feature_pop'] = feature_line[2]
    userobj['feature_proxy'] = feature_line[4]
    userobj['feature_forward'] = feature_line[5]
    userobj['feature_autoreply'] = feature_line[6]
    userobj['feature_rules'] = feature_line[7]
    userobj['feature_modweb'] = feature_line[14]
    userobj['feature_calagent'] = feature_line[19]
    userobj['feature_calsched'] = feature_line[20]
    userobj['feature_virus'] = feature_line[21]

    return Handler.SendDict(req, userobj)

def GetUserFeature(req):
    userdn = req.fields.getfirst('userdn').value.replace('#', '\\')
    index = int(req.fields.getfirst('index').value)
    
    response = ''
    vals = req.mdb.GetAttributes(userdn, msgapi.A_FEATURE_SET)
    for val in vals:
        if val.startswith('A'):
            response = val[index]
            break
    return Handler.SendMessage(req, response)

def SetUserFeature(req):
    userdn = req.fields.getfirst('userdn').value.replace('#', '\\')
    index = int(req.fields.getfirst('index').value)
    value = req.fields.getfirst('value').value

    vals = req.mdb.GetAttributes(userdn, msgapi.A_FEATURE_SET)
    if len(vals) == 0:
        vals = []
        vals.append('AUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUU')
        vals.append('BUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUU')
        vals.append('CUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUU')
        vals.append('DUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUU')
        vals.append('EUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUUU')
    for i in range(len(vals)):
        if vals[i][0] == 'A':
            vals[i] = vals[i][:index] + value + vals[i][index+1:]
            break
    req.mdb.SetAttribute(userdn, msgapi.A_FEATURE_SET, vals)
    return Handler.SendMessage(req, 'Feature set successfully')
