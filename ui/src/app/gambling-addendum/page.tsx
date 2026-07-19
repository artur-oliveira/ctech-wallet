'use client'

import {LegalPage, LegalSection} from '@/components/legal-page'
import {useTranslation} from 'react-i18next'
import {WALLET_TERMS_URL} from '@/lib/legal'

export default function GamblingAddendumPage() {
    const {t} = useTranslation()
    return (
        <LegalPage
            title={t('legal.gambling.v2.title')}
            updatedAt={t('legal.gambling.v2.updatedAt')}
            updatedLabel={t('legal.updatedLabel')}
            metaDescription={t('legal.gambling.v2.metaDescription')}
        >
            <p className="text-xs text-muted-foreground">
                {t('legal.version')} {t('legal.gambling.v2.version')}
            </p>

            <p>
                {t('legal.gambling.v2.intro1')}{' '}
                <a
                    href={WALLET_TERMS_URL}
                    target="_blank"
                    rel="noreferrer"
                    className="underline underline-offset-4"
                >
                    {t('legal.gambling.v2.addendumLink')}
                </a>{' '}
                {t('legal.gambling.v2.intro2')}
            </p>

            <LegalSection heading={t('legal.gambling.v2.s1.heading')}>
                <p>{t('legal.gambling.v2.s1.p1')}</p>
                <p>{t('legal.gambling.v2.s1.p2')}</p>
                <p>{t('legal.gambling.v2.s1.p3')}</p>
                <ul className="list-disc pl-5 space-y-2">
                    <li>{t('legal.gambling.v2.s1.li1')}</li>
                    <li>{t('legal.gambling.v2.s1.li2')}</li>
                    <li>{t('legal.gambling.v2.s1.li3')}</li>
                </ul>
            </LegalSection>

            <LegalSection heading={t('legal.gambling.v2.s2.heading')}>
                <p>{t('legal.gambling.v2.s2.p1')}</p>
                <p>{t('legal.gambling.v2.s2.p2')}</p>
                <p>{t('legal.gambling.v2.s2.p3')}</p>
                <p>{t('legal.gambling.v2.s2.p4')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.gambling.v2.s3.heading')}>
                <p>{t('legal.gambling.v2.s3.p1')}</p>
                <ul className="list-disc pl-5 space-y-2">
                    <li>{t('legal.gambling.v2.s3.li1')}</li>
                    <li>{t('legal.gambling.v2.s3.li2')}</li>
                    <li>{t('legal.gambling.v2.s3.li3')}</li>
                </ul>
                <p>{t('legal.gambling.v2.s3.p2')}</p>
                <p>{t('legal.gambling.v2.s3.p3')}</p>
                <p>{t('legal.gambling.v2.s3.p4')}</p>
                <p>{t('legal.gambling.v2.s3.p5')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.gambling.v2.s4.heading')}>
                <p>{t('legal.gambling.v2.s4.p1')}</p>
                <ul className="list-disc pl-5 space-y-2">
                    <li>{t('legal.gambling.v2.s4.li1')}</li>
                    <li>{t('legal.gambling.v2.s4.li2')}</li>
                    <li>{t('legal.gambling.v2.s4.li3')}</li>
                </ul>
                <p>{t('legal.gambling.v2.s4.p2')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.gambling.v2.s5.heading')}>
                <p>{t('legal.gambling.v2.s5.p1')}</p>
                <p className="font-medium">{t('legal.gambling.v2.s5.p2')}</p>
                <ul className="list-disc pl-5 space-y-2">
                    <li>{t('legal.gambling.v2.s5.li1')}</li>
                    <li>{t('legal.gambling.v2.s5.li2')}</li>
                    <li>{t('legal.gambling.v2.s5.li3')}</li>
                    <li>{t('legal.gambling.v2.s5.li4')}</li>
                    <li>{t('legal.gambling.v2.s5.li5')}</li>
                    <li>{t('legal.gambling.v2.s5.li6')}</li>
                </ul>
            </LegalSection>

            <LegalSection heading={t('legal.gambling.v2.s6.heading')}>
                <p>{t('legal.gambling.v2.s6.p1')}</p>
                <ul className="list-disc pl-5 space-y-2">
                    <li>{t('legal.gambling.v2.s6.li1')}</li>
                    <li>{t('legal.gambling.v2.s6.li2')}</li>
                    <li>{t('legal.gambling.v2.s6.li3')}</li>
                    <li>{t('legal.gambling.v2.s6.li4')}</li>
                    <li>{t('legal.gambling.v2.s6.li5')}</li>
                    <li>{t('legal.gambling.v2.s6.li6')}</li>
                    <li>{t('legal.gambling.v2.s6.li7')}</li>
                    <li>{t('legal.gambling.v2.s6.li8')}</li>
                    <li>{t('legal.gambling.v2.s6.li9')}</li>
                </ul>
            </LegalSection>

            <LegalSection heading={t('legal.gambling.v2.s7.heading')}>
                <p>{t('legal.gambling.v2.s7.p1')}</p>
                <ul className="list-disc pl-5 space-y-2">
                    <li>{t('legal.gambling.v2.s7.li1')}</li>
                    <li>{t('legal.gambling.v2.s7.li2')}</li>
                    <li>{t('legal.gambling.v2.s7.li3')}</li>
                    <li>{t('legal.gambling.v2.s7.li4')}</li>
                    <li>{t('legal.gambling.v2.s7.li5')}</li>
                </ul>
                <p>{t('legal.gambling.v2.s7.p2')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.gambling.v2.s8.heading')}>
                <p>{t('legal.gambling.v2.s8.p1')}</p>
                <p>{t('legal.gambling.v2.s8.p2')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.gambling.v2.s9.heading')}>
                <p>{t('legal.gambling.v2.s9.p1')}</p>
                <p>{t('legal.gambling.v2.s9.p2')}</p>
                <p>{t('legal.gambling.v2.s9.p3')}</p>
                <p>
                    {t('legal.gambling.v2.s9.cvvText')}
                    <strong>{t('legal.gambling.v2.s9.cvvBold')}</strong>
                </p>
                <p>
                    <a
                        href="https://www.cvv.org.br"
                        target="_blank"
                        rel="noreferrer"
                        className="underline underline-offset-4"
                    >
                        {t('legal.gambling.v2.s9.cvvUrl')}
                    </a>
                </p>
                <p>{t('legal.gambling.v2.s9.jaText')}</p>
                <p>
                    <a
                        href="https://jogadoresanonimos.com.br"
                        target="_blank"
                        rel="noreferrer"
                        className="underline underline-offset-4"
                    >
                        {t('legal.gambling.v2.s9.jaUrl')}
                    </a>
                </p>
            </LegalSection>

            <LegalSection heading={t('legal.gambling.v2.s10.heading')}>
                <p>{t('legal.gambling.v2.s10.p1')}</p>
                <p>{t('legal.gambling.v2.s10.p2')}</p>
                <p>{t('legal.gambling.v2.s10.p3')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.gambling.v2.s11.heading')}>
                <p>{t('legal.gambling.v2.s11.p1')}</p>
                <p>{t('legal.gambling.v2.s11.p2')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.gambling.v2.s12.heading')}>
                <p>{t('legal.gambling.v2.s12.p1')}</p>
                <ul className="list-disc pl-5 space-y-2">
                    <li>{t('legal.gambling.v2.s12.li1')}</li>
                    <li>{t('legal.gambling.v2.s12.li2')}</li>
                    <li>{t('legal.gambling.v2.s12.li3')}</li>
                    <li>{t('legal.gambling.v2.s12.li4')}</li>
                    <li>{t('legal.gambling.v2.s12.li5')}</li>
                </ul>
            </LegalSection>
        </LegalPage>
    )
}
