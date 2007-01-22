Dragonfly.FormatTest = function (t)
{
    var d = Dragonfly;
    var f = d.format;

    var fd = d.formatDate;
    var fn = d.formaNumber;
    
    var s = 'hello world!';

    t.is (f (s), s, 'format: no formatting');
    t.is (f ('{0} world!', 'hello'), s, 'format: single replacement');
    t.is (f ('{0} {1}!', 'hello', 'world'), s, 'format: double replacement');
    t.is (f ('{1} {0}!', 'world', 'hello'), s, 'format: swapped double');

    // escaped brace examples from http://blogs.msdn.com/brada/archive/2004/01/14/58851.aspx
    var i = 42;
    t.is (f ('{{ hello to all }}'), '{ hello to all }', 'escaped braces');
    t.is (f ('{0}', i), '42', 'format: number');
    t.is (f ('{{{0}}}', i), '{42}', 'number with escaped braces');
    t.is (f ('{0:N}', i), '42.00', 'number with number format');
    t.is (f ('{{{0:N}}}', i), '{N}', 'number with escaped brace in string format');
    t.is (f ('{0:N!}', i), 'N!', 'bad number format string');

    // from ORA C# Language Pocket Reference p. 98
    // no precision specifiers
    i = 654321;
    t.is (f ('{0:C}', i), '$654,321.00', 'currency, no precision');
    t.is (f ('{0:D}', i), '654321', 'decimal, no precision');
    t.is (f ('{0:E}', i), '6.543210E+005', 'exponent, no precision');
    t.is (f ('{0:F}', i), '654321.00', 'fixed point, no precision');
    t.is (f ('{0:G}', i), '654321', 'general, no precision');
    t.is (f ('{0:N}', i), '654,321.00', 'number, no precision');
    t.is (f ('{0:X}', i), '9FBF1', 'hexadecimal, uppercase');
    t.is (f ('{0:x}', i), '9fbf1', 'hexadecimal, lowercase');

    // precision specifiers
    i = 123;
    t.is (f ('{0:C6}', i), '$123.000000', 'currency with precision');
    t.is (f ('{0:D6}', i), '000123', 'decimal with precision');
    t.is (f ('{0:E6}', i), '1.230000E+002', 'exponent with precision');
    t.is (f ('{0:G6}', i), '123', 'general with precision');
    t.is (f ('{0:N6}', i), '123.000000', 'number with precision');
    t.is (f ('{0:X6}', i), '00007B', 'hexadecimal, uppercase with precision');

    // with a double value
    i = 1.23;
    t.is (f ('{0:C6}', i), '$1.230000', 'currency with double');
    t.is (f ('{0:E6}', i), '1.230000E+000', 'exponent with double');
    t.is (f ('{0:G6}', i), '1.23', 'general with double');
    t.is (f ('{0:N6}', i), '1.230000', 'number with double');

    // picture format specifiers
    i = 123;
    t.is (f ('{0:#0}', i), '123', 'plain picture format');
    t.is (f ('{0:#0;(#0)}', i), '123', 'picture format with negative format');
    t.is (f ('{0:#0;(#0);<zero>}', i), '123', 'picture format with zero and negative formats');
    t.is (f ('{0:#%}', i), '12300%', 'picture format with percentage');

    i = 1.23;
    t.is (f ('{0:#.000E+00}', i), '1.230E+00', 'plain exponent picture format');
    t.is (f ('{0:#.000E+00;(#.000E+00)}', i), '1.230E+00', 'plain exponent picture format with negative format');
    t.is (f ('{0:#.000E+00;($.000E+00);<zero>}', i), '1.230E+00', 'plain exponent picture format with negative and zero formats');
    t.is (f ('{0:#%}', i), '123%', 'exponent picture format with percentage');

    // DateTime format spcifiers
    var dt = new Date (Date.UTC (2000, 9, 11, 15, 32, 14));
    t.is (fd (dt), '10/11/2000 3:32:14 PM', 'Date w/o format string');
    t.is (f ('{0}', dt), '10/11/2000 3:32:14 PM', 'Date with default format string');
    t.is (f ('{0:d}', dt), '10/11/2000', 'Date d: MM/dd/yyyy');
    t.is (f ('{0:D}', dt), 'Wednesday, October 11, 2000', 'Date D: dddd, MMMM dd, yyyy');
    t.is (f ('{0:f}', dt), 'Wednesday, October 11, 2000 3:32 PM', 'Date f: dddd, MMMM dd, yyyy hh:mm tt');
    t.is (f ('{0:F}', dt), 'Wednesday, October 11, 2000 3:32:14 PM', 'Date F: dddd, MMMM dd, yyyy hh:mm:ss tt');
    t.is (f ('{0:g}', dt), '10/11/2000 3:32 PM', 'Date g: MM/dd/yyyy hh:mm tt');
    t.is (f ('{0:G}', dt), '10/11/2000 3:32:14 PM', 'Date G: MM/dd/yyyy HH:mm:ss tt');
    t.is (f ('{0:m}', dt), 'October 11', 'Date m: MMMM dd');
    t.is (f ('{0:M}', dt), 'October 11', 'Date M: MMMM dd');
    t.is (f ('{0:r}', dt), 'Wed, 11 Oct 2000 22:32:14 GMT', 'Date r: ddd, dd MMM yyyy HH:mm:ss "GMT"');
    t.is (f ('{0:R}', dt), 'Wed, 11 Oct 2000 22:32:14 GMT', 'Date R: ddd, dd MMM yyyy HH:mm:ss "GMT"');
    t.is (f ('{0:s}', dt), '2000-10-11T15:32:14', 'Date s: yyyy-MM-ddTHH:mm:ss');
    t.is (f ('{0:S}', dt), '2000-10-11T15:32:14 GMT', 'Date S: yyyy-MM-ddTHH:mm:ss "GMT"');
    t.is (f ('{0:t}', dt), '3:32 PM', 'Date t: hh:mm tt');
    t.is (f ('{0:T}', dt), '3:32:14 PM', 'Date T: hh:mm:ss tt');
    t.is (f ('{0:u}', dt), '2000-10-11 15:32:14Z', 'Date u: yyyy-MM-dd HH:mm:ssZ');
    t.is (f ('{0:U}', dt), 'Wednesday, October 11, 2000 10:32:14 PM', 'Date U: dddd, MMMM dd, yyyy hh:mm:ss tt');
    t.is (f ('{0:y}', dt), 'October 2000', 'MMMM yyyy');
    t.is (f ('{0:Y}', dt), 'October 2000', 'MMMM yyyy');
    t.is (f ('{0:dddd "the" d "day of" MMMM "in the year" yyy}', dt), 'Wednesday the 11 day of October in the year 2000', 'Date custom format');
};
