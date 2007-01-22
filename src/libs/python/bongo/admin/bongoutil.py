import urllib
from bongo import BongoError

class BongoFieldStorage(dict):
    def __init__(self, req):
        self.fields = []        
        if req.headers_out['Content-Type'] == 'multipart/form-data':
            raise BongoError.BongoError( \
                    'multipart/form-data not supported by BongoFieldStorage')

        if req.method == 'GET':
            args = req.args
        elif req.method == 'POST':
            args = req.read()
        else:
            args = None

        if args != None:
            for arg in args.split('&'):
                (key, val) = arg.split('=')
                field = BongoField(key, urllib.unquote_plus(val))
                if self.has_key(key):
                    try:
                        self[key].append(field)
                    except:
                        self[key] = [self[key], field]
                else:
                    self[key] = field
                self.fields.append(field)

    def list(self):
        return self.fields

    def getfirst(self, name, default=None):
        if self.has_key(name):
            try:
                return self[name][0]
            except:
                return self[name]
        return default

    def getlist(self, name):
        fields = []
        try:
            fields.extend(self[name])
        except:
            fields.append(self[name])
        return fields


class BongoField(list):
    def __init__(self, name=None, value=None):
        self.name = name
        self.value = value
        self.file = None
        self.filename = None
        self.type = None
        self.type_options = None
        self.disposition = None
        self.disposition_options = None


def bongoredirect(req, location, permanent=0, text=None):
    if permanent == 1:
        code = 301 # Moved permanently
    elif req.method == 'POST':
        # This fixed POST redirection for me in firefox...
        # hopefully it doesn't have any side-effects.
        code = 303 # See other
    else:
        code = 307 # Temporary redirect
    req.headers_out['Location'] = location
    req.oldreq.send_response(code)
    # something else?

try:
    from mod_python import util as apacheutil
    FieldStorage = apacheutil.FieldStorage
    Field = apacheutil.Field
    redirect = apacheutil.redirect
except ImportError:
    FieldStorage = BongoFieldStorage
    Field = BongoField
    redirect = bongoredirect
