import http from 'k6/http';
import {check, sleep} from 'k6';
export const options = {
    noConnectionReuse: true,
    vus: 50,
    iterations: 50
};

export default function() {
    const params = {
        headers: { 'Authorization': __ENV.TOKEN },
    };
    let res = http.get('http://localhost:8080/api/v9/gateway', params);
    check(res, { 'success': (r) => r.status >= 200 && r.status < 400 });

    let res2 = http.get('http://localhost:8080/api/v9/guilds/203039963636301824', params);
    check(res2, { 'success': (r) => r.status >= 200 && r.status < 400 });

    let res3 = http.get('http://localhost:8080/api/v9/guilds/203039963636301824/channels', params);
    check(res3, { 'success': (r) => r.status >= 200 && r.status < 400 });
}