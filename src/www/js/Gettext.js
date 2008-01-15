var Locale = Locale || {};
Object.extend(Locale, {
  LC_COLLATE:  'C',
  LC_CTYPE:    'C',
  LC_MESSAGES: 'C',
  LC_MONETARY: 'C',
  LC_NUMERIC:  'C',
  LC_TIME:     'C',

  suffix: 'js',
  _messages: {},
  _locale: '',

  setLocale: function(category, locale) {
    switch (category) {
    case 'LC_ALL':
      this['LC_COLLATE']  = locale;
      this['LC_CTYPE']    = locale;
      this['LC_MESSAGES'] = locale;
      this['LC_MONETARY'] = locale;
      this['LC_NUMERIC']  = locale;
      this['LC_TIME']     = locale;
      break;
    case 'LC_COLLATE':
    case 'LC_CTYPE':
    case 'LC_MESSAGES':
    case 'LC_MONETARY':
    case 'LC_NUMERIC':
    case 'LC_TIME':
      this[category] = locale;
      break;
    default:
      return false;
    }
    
    _locale = locale;
  },

  setSuffix: function(suffix) {
    this.suffix = suffix;
  },

  bindTextDomain: function(domain, directory, func, efunc) {
    this._messages = {};

    var uri = '';
    /*if (directory.charAt(0) == '/') {
      uri = location.protocal+'//'+location.host;
    } else if (!directory.match(/^[A-z]:/)) {
      uri = location.href+'/';
    }*/
    /*uri = directory+'/'+this.LC_MESSAGES+'/LC_MESSAGES/'+domain+'.'+this.suffix;*/
	uri = directory + '/'+this.LC_MESSAGES+'.'+this.suffix;
    var options = {
      method: 'get', onSuccess: this._parseJSON.bind(this),
      onComplete: func.bind(this) || function(){},
      onFailure: efunc.bind(this) || function(){}
    };
    
    console.debug('Making request for ' + uri);
    new Ajax.Request(uri, options);
  },

  _parseJSON: function(request) {
    this._messages = eval('('+request.responseText+')');
  },

  getText: function(str) {
    return this._messages[str] || str;
  },

  getTextNoop: function(str) {
    return str;
  }
});

_ = Locale.getText.bind(Locale);
N_ = Locale.getTextNoop;
