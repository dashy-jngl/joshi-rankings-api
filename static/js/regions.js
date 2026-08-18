// Shared birthplace → country → broad-region mapping.
// Cagematch exports German country names (and a few mojibake'd ones — the
// normalized keys below match after stripping non-letters), so canonicalize
// to English before grouping.
(function() {
    'use strict';

    const COUNTRY_FIX = {
        'mexiko': 'Mexico', 'deutschland': 'Germany', 'frankreich': 'France',
        'russland': 'Russia', 'italien': 'Italy', 'sterreich': 'Austria',
        'spanien': 'Spain', 'finnland': 'Finland', 'philippinen': 'Philippines',
        'phillipinen': 'Philippines', 'schweden': 'Sweden', 'indien': 'India',
        'belgien': 'Belgium', 'ungarn': 'Hungary', 'polen': 'Poland',
        'norwegen': 'Norway', 'niederlande': 'Netherlands',
        'griechenland': 'Greece', 'sdkorea': 'South Korea',
        'kolumbien': 'Colombia', 'australien': 'Australia',
        'weissrussland': 'Belarus', 'weiruland': 'Belarus',
        'litauen': 'Lithuania', 'dnemark': 'Denmark', 'singapur': 'Singapore',
        'libanon': 'Lebanon', 'bosnienherzegowina': 'Bosnia',
        'bolivien': 'Bolivia', 'argentinien': 'Argentina',
        'algerien': 'Algeria', 'tschechien': 'Czechia', 'sambia': 'Zambia',
        'mongolei': 'Mongolia', 'dominikanischerepublik': 'Dominican Republic',
        'schweiz': 'Switzerland', 'brasilien': 'Brazil', 'osakajapan': 'Japan',
        'trkei': 'Turkey', 'gypten': 'Egypt', 'sdafrika': 'South Africa',
    };

    const REGION_OF = {
        'Japan': 'Japan',
        'USA': 'North America', 'Canada': 'North America', 'Mexico': 'North America',
        'Costa Rica': 'North America', 'Panama': 'North America',
        'Nicaragua': 'North America', 'Dominican Republic': 'North America',
        'Puerto Rico': 'North America', 'Cuba': 'North America',
        'Brazil': 'South America', 'Chile': 'South America', 'Peru': 'South America',
        'Ecuador': 'South America', 'Colombia': 'South America',
        'Venezuela': 'South America', 'Bolivia': 'South America',
        'Argentina': 'South America', 'Uruguay': 'South America',
        'UK': 'Europe', 'Ireland': 'Europe', 'Germany': 'Europe', 'France': 'Europe',
        'Russia': 'Europe', 'Italy': 'Europe', 'Austria': 'Europe', 'Spain': 'Europe',
        'Finland': 'Europe', 'Sweden': 'Europe', 'Belgium': 'Europe',
        'Hungary': 'Europe', 'Portugal': 'Europe', 'Poland': 'Europe',
        'Norway': 'Europe', 'Netherlands': 'Europe', 'Ukraine': 'Europe',
        'Switzerland': 'Europe', 'Greece': 'Europe', 'Belarus': 'Europe',
        'Lithuania': 'Europe', 'Denmark': 'Europe', 'Bosnia': 'Europe',
        'Czechia': 'Europe', 'Turkey': 'Europe',
        'China': 'Asia', 'Taiwan': 'Asia', 'South Korea': 'Asia', 'India': 'Asia',
        'Philippines': 'Asia', 'Thailand': 'Asia', 'Singapore': 'Asia',
        'Malaysia': 'Asia', 'Mongolia': 'Asia', 'Lebanon': 'Asia',
        'Australia': 'Oceania', 'New Zealand': 'Oceania',
        'South Africa': 'Africa', 'Zambia': 'Africa', 'Algeria': 'Africa',
        'Egypt': 'Africa',
    };

    const REGION_ORDER = ['Japan', 'North America', 'South America', 'Europe',
                          'Asia', 'Oceania', 'Africa', 'Other'];

    function canonCountry(birthplace) {
        if (!birthplace) return null;
        const parts = birthplace.split(',').map(s => s.trim());
        const raw = parts[parts.length - 1];
        if (!raw) return null;
        const norm = raw.toLowerCase().replace(/[^a-z]/g, '');
        return COUNTRY_FIX[norm] || raw;
    }

    function regionOf(country) {
        if (!country) return null;
        return REGION_OF[country] || 'Other';
    }

    window.JoshiRegions = { canonCountry, regionOf, REGION_ORDER };
})();
